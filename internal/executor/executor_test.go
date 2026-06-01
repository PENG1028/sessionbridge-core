package executor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/user/sessionnode/go-core/internal/process"
	"github.com/user/sessionnode/go-core/internal/run"
	"github.com/user/sessionnode/go-core/internal/session"
	"github.com/user/sessionnode/go-core/internal/task"
	"github.com/user/sessionnode/go-core/internal/testutil"
	"github.com/user/sessionnode/go-core/internal/wsconn"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// ---------------------------------------------------------------------------
// test helpers
// ---------------------------------------------------------------------------

// normalize round-trips through JSON to get consistent types for assertion.
func normalize(v interface{}) interface{} {
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		return v
	}
	return out
}

// createTempBinary creates a small executable in a temp dir and prepends that dir to PATH.
// Returns the binary name (without path or extension) for use in check specs.
// Cleans up via t.TempDir() and t.Setenv().
func createTempBinary(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		binPath := filepath.Join(dir, name+".bat")
		if err := os.WriteFile(binPath, []byte("@echo off\r\nexit /b 0\r\n"), 0644); err != nil {
			t.Fatalf("createTempBinary(%s): %v", name, err)
		}
	} else {
		binPath := filepath.Join(dir, name)
		if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
			t.Fatalf("createTempBinary(%s): %v", name, err)
		}
	}
	oldPATH := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+oldPATH)
	return name
}

func testDeps(t *testing.T) *Deps {
	t.Helper()
	sessStore := session.NewStore()
	cr := wsconn.NewRegistry()
	pm := process.NewManager(cr.PushChunk, cr.PushSessionEvent)
	return &Deps{
		Sessions:   sessStore,
		Processes:  pm,
		ConnRoutes: cr,
		Nodes:      &mockNodeLister{},
		TaskStore:  task.NewStore(),
		RunStore:   run.NewStore(),
	}
}

func req(capability string, payload interface{}) *types.CapabilityRequest {
	var raw json.RawMessage
	if payload != nil {
		data, _ := json.Marshal(payload)
		raw = data
	}
	return &types.CapabilityRequest{
		RequestID:  "test_req",
		PluginID:   "test",
		Capability: capability,
		Payload:    raw,
		Actor:      types.Actor{Type: "web", ID: "tester"},
	}
}

// execOK runs Execute and returns a JSON-normalized result.
func execOK(t *testing.T, r *Registry, capability string, payload interface{}) map[string]interface{} {
	t.Helper()
	result, err := r.Execute(req(capability, payload))
	if err != nil {
		t.Fatalf("%s failed: %v", capability, err)
	}
	return normalize(result).(map[string]interface{})
}

// extractSid creates a session and returns its ID.
func extractSid(t *testing.T, r *Registry) string {
	t.Helper()
	return execOK(t, r, "session.create", map[string]string{"command": "bash"})["sessionId"].(string)
}

type mockNodeLister struct{}

func (m *mockNodeLister) ListNodes() []NodeInfo {
	return []NodeInfo{
		{ID: "node-local", Name: "local", Status: "local"},
		{ID: "node-peer", Name: "peer", Status: "connected"},
	}
}

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

func TestRegistry_Execute_UnknownCapability(t *testing.T) {
	r := New(testDeps(t))
	_, err := r.Execute(req("nonexistent.capability", nil))
	if err == nil {
		t.Fatal("expected error for unknown capability")
	}
	if !strings.Contains(err.Error(), "unknown capability") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRegistry_RegisterAndExecute(t *testing.T) {
	r := New(testDeps(t))
	r.Register("test.echo", func(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
		return map[string]interface{}{"echo": "ok"}, nil
	})
	m := execOK(t, r, "test.echo", nil)
	if m["echo"] != "ok" {
		t.Errorf("got %v, want ok", m["echo"])
	}
}

// ---------------------------------------------------------------------------
// session.*
// ---------------------------------------------------------------------------

func TestSessionCreate(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "session.create", map[string]string{
		"command":  "bash",
		"cwd":      "/tmp",
		"pluginId": "shell",
	})
	if m["sessionId"] == nil || m["sessionId"] == "" {
		t.Error("sessionId is empty")
	}
	if m["state"] != "created" {
		t.Errorf("state = %v, want created", m["state"])
	}
}

func TestSessionCreate_EmptyPayload(t *testing.T) {
	r := New(testDeps(t))
	_, err := r.Execute(req("session.create", nil))
	if err != nil {
		t.Fatalf("session.create with nil payload: %v", err)
	}
}

func TestSessionDestroy(t *testing.T) {
	r := New(testDeps(t))
	sid := extractSid(t, r)
	m := execOK(t, r, "session.destroy", map[string]string{"sessionId": sid})
	if m["status"] != "destroyed" {
		t.Errorf("status = %v, want destroyed", m["status"])
	}
}

func TestSessionDestroy_MissingID(t *testing.T) {
	r := New(testDeps(t))
	_, err := r.Execute(req("session.destroy", map[string]string{}))
	if err == nil {
		t.Fatal("expected error for missing sessionId")
	}
}

func TestSessionDestroy_NotFound(t *testing.T) {
	r := New(testDeps(t))
	_, err := r.Execute(req("session.destroy", map[string]string{"sessionId": "nonexistent"}))
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestSessionList(t *testing.T) {
	r := New(testDeps(t))
	extractSid(t, r)
	extractSid(t, r)

	m := execOK(t, r, "session.list", nil)
	sessions := m["sessions"].([]interface{})
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(sessions))
	}
}

func TestSessionInfo(t *testing.T) {
	r := New(testDeps(t))
	sid := execOK(t, r, "session.create", map[string]string{
		"command":  "bash",
		"cwd":      "/home",
		"pluginId": "shell",
	})["sessionId"].(string)

	m := execOK(t, r, "session.info", map[string]string{"sessionId": sid})
	if m["sessionId"] != sid {
		t.Errorf("sessionId = %v, want %s", m["sessionId"], sid)
	}
	if m["state"] != "created" {
		t.Errorf("state = %v", m["state"])
	}
	if m["command"] != "bash" {
		t.Errorf("command = %v", m["command"])
	}
}

func TestSessionInfo_MissingID(t *testing.T) {
	r := New(testDeps(t))
	_, err := r.Execute(req("session.info", map[string]string{}))
	if err == nil {
		t.Fatal("expected error for missing sessionId")
	}
}

func TestSessionInfo_NotFound(t *testing.T) {
	r := New(testDeps(t))
	_, err := r.Execute(req("session.info", map[string]string{"sessionId": "nonexistent"}))
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

// ---------------------------------------------------------------------------
// stream.*
// ---------------------------------------------------------------------------

func TestStreamWriteAndSubscribe(t *testing.T) {
	r := New(testDeps(t))
	sid := extractSid(t, r)

	wm := execOK(t, r, "stream.write", map[string]string{
		"sessionId": sid,
		"stream":    "stdout",
		"data":      "hello world",
	})
	if wm["written"].(float64) != 11 {
		t.Errorf("written = %v, want 11", wm["written"])
	}

	sm := execOK(t, r, "stream.subscribe", map[string]string{
		"sessionId": sid,
		"stream":    "stdout",
	})
	if sm["data"] != "hello world" {
		t.Errorf("data = %v, want 'hello world'", sm["data"])
	}
}

func TestStreamSubscribe_MissingID(t *testing.T) {
	r := New(testDeps(t))
	_, err := r.Execute(req("stream.subscribe", map[string]string{"stream": "stdout"}))
	if err == nil {
		t.Fatal("expected error for missing sessionId")
	}
}

func TestStreamSubscribe_UnknownStream(t *testing.T) {
	r := New(testDeps(t))
	sid := extractSid(t, r)
	m, err := r.Execute(req("stream.subscribe", map[string]string{
		"sessionId": sid,
		"stream":    "nonexistent",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	res := m.(map[string]interface{})
	if res["subscriptionId"] == "" {
		t.Fatal("expected non-empty subscriptionId")
	}
	if res["sessionId"] != sid {
		t.Errorf("sessionId = %v, want %s", res["sessionId"], sid)
	}
}

func TestStreamList(t *testing.T) {
	r := New(testDeps(t))
	sid := extractSid(t, r)
	m := execOK(t, r, "stream.list", map[string]string{"sessionId": sid})
	streams := m["streams"].([]interface{})
	if len(streams) == 0 {
		t.Error("expected at least one stream")
	}
}

// ---------------------------------------------------------------------------
// process.*
// ---------------------------------------------------------------------------

func TestProcessSpawn(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "process.spawn", map[string]interface{}{
		"command": "go",
		"args":    []string{"version"},
	})
	if m["sessionId"] == nil || m["sessionId"] == "" {
		t.Error("missing sessionId")
	}
	if m["state"] != "running" {
		t.Errorf("state = %v, want running", m["state"])
	}
}

func TestProcessSpawn_EmptyCommand(t *testing.T) {
	r := New(testDeps(t))
	// Empty command now defaults to shell ("bash" on unix, "cmd" on windows).
	m := execOK(t, r, "process.spawn", map[string]string{})
	if m["sessionId"] == nil || m["sessionId"] == "" {
		t.Error("missing sessionId for default shell spawn")
	}
}

func TestProcessSpawn_BadCommand(t *testing.T) {
	r := New(testDeps(t))
	_, err := r.Execute(req("process.spawn", map[string]string{
		"command": "nonexistent_cmd_xyz",
	}))
	if err == nil {
		t.Fatal("expected error for bad command")
	}
}

func TestProcessSpawnAndSignal(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "process.spawn", map[string]interface{}{
		"command": "go",
		"args":    []string{"version"},
	})
	sid := m["sessionId"].(string)

	_, err := r.Execute(req("process.signal", map[string]string{
		"sessionId": sid,
		"signal":    "SIGTERM",
	}))
	if err != nil {
		t.Fatalf("process.signal failed: %v", err)
	}
}

func TestProcessSignal_MissingID(t *testing.T) {
	r := New(testDeps(t))
	_, err := r.Execute(req("process.signal", map[string]string{
		"signal": "SIGTERM",
	}))
	if err == nil {
		t.Fatal("expected error for missing sessionId")
	}
}

func TestProcessList(t *testing.T) {
	r := New(testDeps(t))
	r.Execute(req("process.spawn", map[string]interface{}{
		"command": "go", "args": []string{"version"},
	}))
	r.Execute(req("process.spawn", map[string]interface{}{
		"command": "go", "args": []string{"env"},
	}))

	m := execOK(t, r, "process.list", nil)
	procs := m["processes"].([]interface{})
	if len(procs) != 2 {
		t.Errorf("expected 2 processes, got %d", len(procs))
	}
}

// ---------------------------------------------------------------------------
// env.*
// ---------------------------------------------------------------------------

func TestEnvGet(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "env.get", map[string]string{"name": "PATH"})
	if m["name"] != "PATH" {
		t.Errorf("name = %v", m["name"])
	}
	if m["found"] != true {
		t.Errorf("found = %v, want true", m["found"])
	}
	if m["value"] == "" {
		t.Error("PATH should not be empty")
	}
}

func TestEnvGet_MissingName(t *testing.T) {
	r := New(testDeps(t))
	_, err := r.Execute(req("env.get", map[string]string{}))
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestEnvGet_NotFound(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "env.get", map[string]string{"name": "THIS_VAR_PROBABLY_DOES_NOT_EXIST_XYZ"})
	if m["found"] != false {
		t.Errorf("found = %v, want false", m["found"])
	}
}

func TestEnvSetAndUnset(t *testing.T) {
	r := New(testDeps(t))

	_, err := r.Execute(req("env.set", map[string]string{"name": "TEST_VAR", "value": "test_val"}))
	if err != nil {
		t.Fatalf("env.set failed: %v", err)
	}

	m := execOK(t, r, "env.get", map[string]string{"name": "TEST_VAR"})
	if m["value"] != "test_val" {
		t.Errorf("value = %v, want test_val", m["value"])
	}

	_, err = r.Execute(req("env.unset", map[string]string{"name": "TEST_VAR"}))
	if err != nil {
		t.Fatalf("env.unset failed: %v", err)
	}
}

func TestEnvList(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "env.list", nil)
	if m["count"].(float64) == 0 {
		t.Error("expected non-zero env count")
	}
}

// ---------------------------------------------------------------------------
// system.*
// ---------------------------------------------------------------------------

func TestSystemInfo(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "system.info", nil)
	if m["os"] == nil {
		t.Error("missing os field")
	}
	if m["arch"] == nil {
		t.Error("missing arch field")
	}
	if m["hostname"] == nil {
		t.Error("missing hostname field")
	}
}

// ---------------------------------------------------------------------------
// fs.* (with temp files)
// ---------------------------------------------------------------------------

func TestFsWriteAndRead(t *testing.T) {
	r := New(testDeps(t))
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")

	_, err := r.Execute(req("fs.write", map[string]string{
		"path": testFile,
		"data": "hello fs",
	}))
	if err != nil {
		t.Fatalf("fs.write failed: %v", err)
	}

	m := execOK(t, r, "fs.read", map[string]string{"path": testFile})
	if m["data"] != "hello fs" {
		t.Errorf("data = %v, want 'hello fs'", m["data"])
	}
	if m["size"].(float64) != 8 {
		t.Errorf("size = %v, want 8", m["size"])
	}
}

func TestFsRead_MissingPath(t *testing.T) {
	r := New(testDeps(t))
	_, err := r.Execute(req("fs.read", map[string]string{}))
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestFsRead_NotFound(t *testing.T) {
	r := New(testDeps(t))
	_, err := r.Execute(req("fs.read", map[string]string{"path": "/nonexistent/path/xyz"}))
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestFsList(t *testing.T) {
	r := New(testDeps(t))
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "b.txt"), []byte("b"), 0644)
	os.MkdirAll(filepath.Join(tmpDir, "sub"), 0755)

	m := execOK(t, r, "fs.list", map[string]string{"path": tmpDir})
	entries := m["entries"].([]interface{})
	if len(entries) != 3 {
		t.Errorf("expected 3 entries, got %d", len(entries))
	}
}

func TestFsList_DefaultPath(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "fs.list", nil)
	if m["path"] != "." {
		t.Errorf("path = %v, want .", m["path"])
	}
}

// ---------------------------------------------------------------------------
// node.* (via NodeLister mock)
// ---------------------------------------------------------------------------

func TestNodeList(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "node.list", nil)
	nodes := m["nodes"].([]interface{})
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(nodes))
	}
}

func TestNodeHealth(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "node.health", nil)
	if m["status"] != "ok" {
		t.Errorf("status = %v, want ok", m["status"])
	}
	if m["nodeId"] == nil || m["nodeId"] == "" {
		t.Error("missing nodeId")
	}
}

// ---------------------------------------------------------------------------
// env extra helpers
// ---------------------------------------------------------------------------

func TestEnvCheckBinary(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "env.checkBinary", map[string]string{"name": "go"})
	if m["found"] != true {
		t.Errorf("found = %v, want true", m["found"])
	}
}

func TestEnvCheckBinary_NotFound(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "env.checkBinary", map[string]string{"name": "nonexistent_binary_xyz_123"})
	if m["found"] != false {
		t.Errorf("found = %v, want false", m["found"])
	}
}

func TestEnvWhich(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "env.which", map[string]string{"name": "go"})
	if m["path"] == "" {
		t.Error("expected non-empty path for go")
	}
}

func TestEnvHome(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "env.home", nil)
	if m["home"] == "" {
		t.Error("expected non-empty home")
	}
}

func TestEnvCwd(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "env.cwd", nil)
	if m["cwd"] == "" {
		t.Error("expected non-empty cwd")
	}
}

// ---------------------------------------------------------------------------
// edge cases
// ---------------------------------------------------------------------------

func TestPayloadDecode_EmptyPayload(t *testing.T) {
	r := New(testDeps(t))
	_, err := r.Execute(req("node.list", nil))
	if err != nil {
		t.Fatalf("node.list with nil payload: %v", err)
	}
}

func TestSessionGet(t *testing.T) {
	r := New(testDeps(t))
	sid := extractSid(t, r)
	m := execOK(t, r, "session.get", map[string]string{"sessionId": sid})
	if m["sessionId"] != sid {
		t.Errorf("sessionId = %v", m["sessionId"])
	}
}

func TestLifecycle_SessionCreateToDestroy(t *testing.T) {
	r := New(testDeps(t))
	sid := execOK(t, r, "session.create", map[string]string{
		"command": "sleep", "pluginId": "test-plugin",
	})["sessionId"].(string)

	list1 := execOK(t, r, "session.list", nil)
	if len(list1["sessions"].([]interface{})) != 1 {
		t.Fatal("expected 1 session after create")
	}

	execOK(t, r, "session.destroy", map[string]string{"sessionId": sid})

	list2 := execOK(t, r, "session.list", nil)
	if len(list2["sessions"].([]interface{})) != 0 {
		t.Fatal("expected 0 sessions after destroy")
	}
}

func TestStreamWriteToStdinWithProcess(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	catBin := testutil.CatBinary(t)
	m := execOK(t, r, "process.spawn", map[string]interface{}{"command": catBin})
	sid := m["sessionId"].(string)
	time.Sleep(100 * time.Millisecond)

	wm := execOK(t, r, "stream.write", map[string]string{
		"sessionId": sid,
		"stream":    "stdin",
		"data":      "hello stdin\n",
	})
	if wm["written"].(float64) == 0 {
		t.Error("expected >0 bytes written")
	}

	// Close stdin so the process can exit (prevents TempDir cleanup failure on Windows)
	deps.Processes.CloseStdin(types.SessionID(sid))
}

func TestStreamWriteWithStreamType(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	catBin := testutil.CatBinary(t)
	m := execOK(t, r, "process.spawn", map[string]interface{}{"command": catBin})
	sid := m["sessionId"].(string)
	time.Sleep(100 * time.Millisecond)

	// Use canonical "streamType" field instead of legacy "stream"
	wm := execOK(t, r, "stream.write", map[string]string{
		"sessionId":  sid,
		"streamType": "stdin",
		"data":       "hello streamType\n",
	})
	if wm["written"].(float64) == 0 {
		t.Error("expected >0 bytes written")
	}
	if wm["streamType"].(string) != "stdin" {
		t.Errorf("streamType = %v, want 'stdin'", wm["streamType"])
	}

	// Close stdin so the process can exit (prevents TempDir cleanup failure on Windows)
	deps.Processes.CloseStdin(types.SessionID(sid))
}

// ---------------------------------------------------------------------------
// process manager edge cases
// ---------------------------------------------------------------------------

func TestProcessWriteStdin(t *testing.T) {
	deps := testDeps(t)
	pm := deps.Processes
	catBin := testutil.CatBinary(t)
	sid, err := pm.Spawn(catBin, nil, "", nil)
	if err != nil {
		t.Fatalf("spawn cat failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := pm.WriteStdin(sid, "hello\n"); err != nil {
		t.Fatalf("WriteStdin failed: %v", err)
	}
	if err := pm.CloseStdin(sid); err != nil {
		t.Fatalf("CloseStdin failed: %v", err)
	}
}

func TestProcessSignal_Kill(t *testing.T) {
	deps := testDeps(t)
	pm := deps.Processes
	sleepBin := testutil.SleepBinary(t)
	sid, err := pm.Spawn(sleepBin, []string{"30"}, "", nil)
	if err != nil {
		t.Fatalf("spawn sleep failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := pm.Signal(sid, "SIGKILL", false); err != nil {
		t.Fatalf("Signal failed: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	proc := pm.Get(sid)
	if proc.State != "exited" {
		t.Errorf("state = %s, want exited", proc.State)
	}
}

func TestProcessSignal_NotFound(t *testing.T) {
	deps := testDeps(t)
	err := deps.Processes.Signal("nonexistent", "SIGTERM", false)
	if err == nil {
		t.Fatal("expected error for nonexistent process")
	}
}

func TestProcessWriteStdin_NotFound(t *testing.T) {
	deps := testDeps(t)
	err := deps.Processes.WriteStdin("nonexistent", "data")
	if err == nil {
		t.Fatal("expected error for nonexistent process")
	}
}

func TestProcessList_Empty(t *testing.T) {
	deps := testDeps(t)
	procs := deps.Processes.List()
	if len(procs) != 0 {
		t.Errorf("expected 0 processes, got %d", len(procs))
	}
}

func TestProcessCleanup(t *testing.T) {
	deps := testDeps(t)
	pm := deps.Processes
	sleepBin := testutil.SleepBinary(t)
	pm.Spawn(sleepBin, []string{"30"}, "", nil)
	pm.Spawn(sleepBin, []string{"30"}, "", nil)
	if pm.Count() != 2 {
		t.Fatalf("expected 2 processes, got %d", pm.Count())
	}
	pm.Cleanup()
	if pm.Count() != 0 {
		t.Errorf("expected 0 after cleanup, got %d", pm.Count())
	}
}

// ---------------------------------------------------------------------------
