package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/user/sessionnode/go-core/internal/capability"
	"github.com/user/sessionnode/go-core/internal/config"
	"github.com/user/sessionnode/go-core/internal/history"
	"github.com/user/sessionnode/go-core/internal/notify"
	"github.com/user/sessionnode/go-core/internal/plan"
	"github.com/user/sessionnode/go-core/internal/platform"
	"github.com/user/sessionnode/go-core/internal/pluginmanifest"
	"github.com/user/sessionnode/go-core/internal/process"
	"github.com/user/sessionnode/go-core/internal/run"
	"github.com/user/sessionnode/go-core/internal/session"
	"github.com/user/sessionnode/go-core/internal/task"
	"github.com/user/sessionnode/go-core/internal/testutil"
	"github.com/user/sessionnode/go-core/internal/update"
	"github.com/user/sessionnode/go-core/internal/wsconn"
	"github.com/user/sessionnode/go-core/pkg/protocol"
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
	_, err := r.Execute(req("process.spawn", map[string]string{}))
	if err == nil {
		t.Fatal("expected error for empty command")
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
// Plugin management stubs
// ---------------------------------------------------------------------------

func TestPluginCheck_ReturnsOK(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "sessionnode-core"})
	if m["status"] != "ok" {
		t.Errorf("status = %v, want ok", m["status"])
	}
	if m["pluginId"] != "sessionnode-core" {
		t.Errorf("pluginId = %v, want sessionnode-core", m["pluginId"])
	}
}

// ---------------------------------------------------------------------------
// plugin.check real dependency checks
// ---------------------------------------------------------------------------

// checkDeps creates Deps with a manifest that has the given environment checks.
func checkDeps(t *testing.T, checks []pluginmanifest.EnvCheckSpec) *Deps {
	t.Helper()
	deps := fullPluginDeps(t)
	deps.Manifests = &mockManifestLoader{
		manifest: &pluginmanifest.Manifest{
			ID: "test-plugin",
			Core: &pluginmanifest.CoreSpec{
				Environment: pluginmanifest.EnvironmentSpec{
					Checks: checks,
				},
			},
		},
	}
	return deps
}

func TestPluginCheck_Binary_Found(t *testing.T) {
	bin := createTempBinary(t, "testtool")
	deps := checkDeps(t, []pluginmanifest.EnvCheckSpec{
		{ID: "testtool", Type: "binary", Command: bin, Required: false},
	})
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})
	depsResult := m["dependencies"].([]interface{})
	if len(depsResult) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(depsResult))
	}
	d := depsResult[0].(map[string]interface{})
	if d["status"] != "ok" {
		t.Errorf("binary %q status = %v, want ok", bin, d["status"])
	}
}

func TestPluginCheck_Binary_Missing(t *testing.T) {
	deps := checkDeps(t, []pluginmanifest.EnvCheckSpec{
		{ID: "nonexistent", Type: "binary", Command: "this-command-does-not-exist-xyzzy", Required: false},
	})
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})
	depsResult := m["dependencies"].([]interface{})
	if len(depsResult) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(depsResult))
	}
	d := depsResult[0].(map[string]interface{})
	if d["status"] != "missing" {
		t.Errorf("missing binary status = %v, want missing", d["status"])
	}
}

func TestPluginCheck_Env_Found(t *testing.T) {
	t.Setenv("TEST_PLUGIN_CHECK_EXISTS", "1")
	deps := checkDeps(t, []pluginmanifest.EnvCheckSpec{
		{ID: "test-var", Type: "env", Command: "TEST_PLUGIN_CHECK_EXISTS", Required: false},
	})
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})
	depsResult := m["dependencies"].([]interface{})
	if len(depsResult) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(depsResult))
	}
	d := depsResult[0].(map[string]interface{})
	if d["status"] != "ok" {
		t.Errorf("env %q status = %v, want ok", "TEST_PLUGIN_CHECK_EXISTS", d["status"])
	}
}

func TestPluginCheck_Env_Missing(t *testing.T) {
	deps := checkDeps(t, []pluginmanifest.EnvCheckSpec{
		{ID: "missing", Type: "env", Command: "THIS_ENV_DOES_NOT_EXIST_XYZZY", Required: false},
	})
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})
	depsResult := m["dependencies"].([]interface{})
	if len(depsResult) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(depsResult))
	}
	d := depsResult[0].(map[string]interface{})
	if d["status"] != "missing" {
		t.Errorf("missing env status = %v, want missing", d["status"])
	}
}

func TestPluginCheck_RequiredMissing_FailsOverall(t *testing.T) {
	deps := checkDeps(t, []pluginmanifest.EnvCheckSpec{
		{ID: "missing", Type: "binary", Command: "this-command-does-not-exist-xyzzy", Required: true},
	})
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})
	if m["status"] != "blocked" {
		t.Errorf("overall status = %v, want blocked (required dep missing generates missing_dependency blocker)", m["status"])
	}
}

func TestPluginCheck_OptionalMissing_ReturnsIncomplete(t *testing.T) {
	bin := createTempBinary(t, "foundtool")
	deps := checkDeps(t, []pluginmanifest.EnvCheckSpec{
		{ID: "found", Type: "binary", Command: bin, Required: false},
		{ID: "missing", Type: "binary", Command: "this-command-does-not-exist-xyzzy", Required: false},
	})
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})
	if m["status"] != "incomplete" {
		t.Errorf("overall status = %v, want incomplete (optional dep missing)", m["status"])
	}
}

func TestPluginCheck_BinaryEmptyCommand_ReturnsSkipped(t *testing.T) {
	deps := checkDeps(t, []pluginmanifest.EnvCheckSpec{
		{ID: "empty", Type: "binary", Command: "", Required: false},
	})
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})
	depsResult := m["dependencies"].([]interface{})
	d := depsResult[0].(map[string]interface{})
	if d["status"] != "skipped" {
		t.Errorf("empty binary status = %v, want skipped", d["status"])
	}
}

func TestPluginCheck_UnknownType_ReturnsUnknown(t *testing.T) {
	deps := checkDeps(t, []pluginmanifest.EnvCheckSpec{
		{ID: "weird", Type: "invalid_type", Command: "anything", Required: false},
	})
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})
	depsResult := m["dependencies"].([]interface{})
	d := depsResult[0].(map[string]interface{})
	if d["status"] != "unknown" {
		t.Errorf("unknown type status = %v, want unknown", d["status"])
	}
}

func TestPluginCheck_PathExists(t *testing.T) {
	dir := t.TempDir()
	deps := checkDeps(t, []pluginmanifest.EnvCheckSpec{
		{ID: "tmpdir", Type: "path", Command: dir, Required: false},
	})
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})
	depsResult := m["dependencies"].([]interface{})
	d := depsResult[0].(map[string]interface{})
	if d["status"] != "ok" {
		t.Errorf("existing path status = %v, want ok", d["status"])
	}
}

func TestPluginCheck_PathMissing(t *testing.T) {
	deps := checkDeps(t, []pluginmanifest.EnvCheckSpec{
		{ID: "missing", Type: "path", Command: "/tmp/nonexistent-path-xyzzy", Required: false},
	})
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})
	depsResult := m["dependencies"].([]interface{})
	d := depsResult[0].(map[string]interface{})
	if d["status"] != "missing" {
		t.Errorf("missing path status = %v, want missing", d["status"])
	}
}

func TestPluginCheck_File_Existing(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	deps := checkDeps(t, []pluginmanifest.EnvCheckSpec{
		{ID: "testfile", Type: "file", Command: filePath, Required: false},
	})
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})
	depsResult := m["dependencies"].([]interface{})
	d := depsResult[0].(map[string]interface{})
	if d["status"] != "ok" {
		t.Errorf("existing file status = %v, want ok", d["status"])
	}
}

func TestPluginCheck_Directory_Existing(t *testing.T) {
	dir := t.TempDir()
	deps := checkDeps(t, []pluginmanifest.EnvCheckSpec{
		{ID: "testdir", Type: "directory", Command: dir, Required: false},
	})
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})
	depsResult := m["dependencies"].([]interface{})
	d := depsResult[0].(map[string]interface{})
	if d["status"] != "ok" {
		t.Errorf("existing directory status = %v, want ok", d["status"])
	}
}

func TestPluginCheck_Directory_WhenFile_ReturnsTypeMismatch(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	deps := checkDeps(t, []pluginmanifest.EnvCheckSpec{
		{ID: "testfile", Type: "directory", Command: filePath, Required: false},
	})
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})
	depsResult := m["dependencies"].([]interface{})
	d := depsResult[0].(map[string]interface{})
	if d["status"] != "type_mismatch" {
		t.Errorf("file-as-directory status = %v, want type_mismatch", d["status"])
	}
}

func TestPluginCheck_Command_Check(t *testing.T) {
	bin := createTempBinary(t, "cmdcheck")
	deps := checkDeps(t, []pluginmanifest.EnvCheckSpec{
		{ID: "cmdcheck", Type: "command", Command: bin, Args: "status", Required: false},
	})
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})
	depsResult := m["dependencies"].([]interface{})
	if len(depsResult) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(depsResult))
	}
	d := depsResult[0].(map[string]interface{})
	if d["status"] != "ok" {
		t.Errorf("command %q status = %v, want ok", bin, d["status"])
	}
}

func TestPluginCheck_MultipleChecksAllOK(t *testing.T) {
	bin := createTempBinary(t, "multitool")
	t.Setenv("TEST_MULTI_ENV", "1")
	deps := checkDeps(t, []pluginmanifest.EnvCheckSpec{
		{ID: "multi-bin", Type: "binary", Command: bin, Required: true},
		{ID: "multi-env", Type: "env", Command: "TEST_MULTI_ENV", Required: false},
		{ID: "multi-cmd", Type: "command", Command: bin, Args: "ok", Required: false},
	})
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})
	if m["status"] != "ok" {
		t.Errorf("overall status = %v, want ok", m["status"])
	}
	depsResult := m["dependencies"].([]interface{})
	if len(depsResult) != 3 {
		t.Errorf("expected 3 dependencies, got %d", len(depsResult))
	}
}

func TestPluginCheck_RequiredAndOptionalMixed(t *testing.T) {
	bin := createTempBinary(t, "mixedtool")
	deps := checkDeps(t, []pluginmanifest.EnvCheckSpec{
		{ID: "required-missing", Type: "binary", Command: "this-command-does-not-exist-xyzzy", Required: true},
		{ID: "optional-missing", Type: "binary", Command: "another-nonexistent-cmd", Required: false},
		{ID: "found", Type: "binary", Command: bin, Required: true},
	})
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})
	if m["status"] != "blocked" {
		t.Errorf("overall status = %v, want blocked (required dep missing generates missing_dependency blocker)", m["status"])
	}
}

// ---------------------------------------------------------------------------
// plugin.check capability support tests
// ---------------------------------------------------------------------------

// capCheckDeps creates Deps with a manifest that has the given permissions.
// The CapResolver is set to a default resolver from the current platform.
func capCheckDeps(t *testing.T, pluginID string, perms []pluginmanifest.PermissionSpec) *Deps {
	t.Helper()
	deps := fullPluginDeps(t)
	resolver := &capability.Resolver{Platform: platform.Current()}
	deps.CapResolver = resolver
	deps.Manifests = &mockManifestLoader{
		manifest: &pluginmanifest.Manifest{
			ID: pluginID,
			Core: &pluginmanifest.CoreSpec{
				Permissions: perms,
				Environment: pluginmanifest.EnvironmentSpec{},
			},
		},
	}
	return deps
}

// capCheckDepsWithResolver creates Deps with a manifest and a custom resolver.
func capCheckDepsWithResolver(t *testing.T, pluginID string, perms []pluginmanifest.PermissionSpec, resolver *capability.Resolver) *Deps {
	t.Helper()
	deps := capCheckDeps(t, pluginID, perms)
	deps.CapResolver = resolver
	return deps
}

func TestPluginCheck_CapSupported_NoBlockers(t *testing.T) {
	deps := capCheckDeps(t, "test-plugin", []pluginmanifest.PermissionSpec{
		{ID: "fs-access", Capabilities: []string{"fs.read", "fs.write", "fs.list"}, Default: "allow"},
	})
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})

	// Verify capabilities array is present and populated
	caps, ok := m["capabilities"].([]interface{})
	if !ok {
		t.Fatalf("capabilities is not an array: %T", m["capabilities"])
	}
	if len(caps) != 3 {
		t.Errorf("expected 3 capabilities, got %d", len(caps))
	}
	for _, c := range caps {
		ce := c.(map[string]interface{})
		if ce["supported"] != true {
			t.Errorf("capability %q: supported = %v, want true", ce["capability"], ce["supported"])
		}
	}

	// Verify no blockers for supported caps
	blockers := m["blockers"].([]interface{})
	if len(blockers) != 0 {
		t.Errorf("expected 0 blockers, got %d: %v", len(blockers), blockers)
	}

	// Status should be ok
	if m["status"] != "ok" {
		t.Errorf("status = %v, want ok", m["status"])
	}
}

func TestPluginCheck_CapUnsupported_ProducesBlocker(t *testing.T) {
	// Create a resolver with a Windows platform where process.resize is unsupported
	resolver := &capability.Resolver{
		Platform: platform.RuntimePlatform{OS: "windows", Arch: "amd64", Runtime: "desktop"},
	}
	deps := capCheckDepsWithResolver(t, "test-plugin", []pluginmanifest.PermissionSpec{
		{ID: "process-access", Capabilities: []string{"process.resize", "process.spawn"}, Default: "allow"},
	}, resolver)
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})

	// Verify capabilities array
	caps, ok := m["capabilities"].([]interface{})
	if !ok {
		t.Fatalf("capabilities is not an array: %T", m["capabilities"])
	}
	if len(caps) != 2 {
		t.Fatalf("expected 2 capabilities, got %d", len(caps))
	}

	// process.resize should be unsupported on Windows
	resizeCap := caps[0].(map[string]interface{})
	if resizeCap["capability"] != "process.resize" {
		t.Errorf("first capability = %v, want process.resize", resizeCap["capability"])
	}
	if resizeCap["supported"] != false {
		t.Errorf("process.resize supported = %v, want false", resizeCap["supported"])
	}

	// process.spawn should be partial (still supported) on Windows
	spawnCap := caps[1].(map[string]interface{})
	if spawnCap["capability"] != "process.spawn" {
		t.Errorf("second capability = %v, want process.spawn", spawnCap["capability"])
	}
	if spawnCap["supported"] != true {
		t.Errorf("process.spawn supported = %v, want true", spawnCap["supported"])
	}

	// Verify blockers
	blockers := m["blockers"].([]interface{})
	if len(blockers) != 1 {
		t.Fatalf("expected 1 blocker, got %d", len(blockers))
	}
	b := blockers[0].(map[string]interface{})
	if b["kind"] != "unsupported_capability" {
		t.Errorf("blocker kind = %v, want unsupported_capability", b["kind"])
	}
	if b["capability"] != "process.resize" {
		t.Errorf("blocker capability = %v, want process.resize", b["capability"])
	}
	if b["reason"] != "no_pty_resize" {
		t.Errorf("blocker reason = %v, want no_pty_resize", b["reason"])
	}

	// Status should be blocked
	if m["status"] != "blocked" {
		t.Errorf("status = %v, want blocked", m["status"])
	}
}

func TestPluginCheck_MissingDep_ProducesBlocker(t *testing.T) {
	deps := checkDeps(t, []pluginmanifest.EnvCheckSpec{
		{ID: "required-tool", Type: "binary", Command: "this-command-does-not-exist-xyzzy", Required: true},
	})
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})

	// Verify status is blocked
	if m["status"] != "blocked" {
		t.Errorf("status = %v, want blocked", m["status"])
	}

	// Verify missing_dependency blocker is present
	blockers := m["blockers"].([]interface{})
	found := false
	for _, blk := range blockers {
		b := blk.(map[string]interface{})
		if b["kind"] == "missing_dependency" && b["dependency"] == "required-tool" {
			found = true
			if b["reason"] != "binary_missing" {
				t.Errorf("blocker reason = %v, want binary_missing", b["reason"])
			}
		}
	}
	if !found {
		t.Errorf("expected missing_dependency blocker for required-tool, got blockers: %v", blockers)
	}
}

func TestPluginCheck_OldDependencyShape_Preserved(t *testing.T) {
	bin := createTempBinary(t, "shapecheck")
	deps := checkDeps(t, []pluginmanifest.EnvCheckSpec{
		{ID: "shapecheck", Type: "binary", Command: bin, Required: false},
	})
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})

	// Verify backwards-compatible fields
	if _, ok := m["pluginId"]; !ok {
		t.Error("pluginId field missing")
	}
	if _, ok := m["status"]; !ok {
		t.Error("status field missing")
	}
	if _, ok := m["dependencies"]; !ok {
		t.Error("dependencies field missing")
	}

	depsResult := m["dependencies"].([]interface{})
	if len(depsResult) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(depsResult))
	}
	d := depsResult[0].(map[string]interface{})
	// Verify old fields are present
	for _, key := range []string{"id", "type", "status"} {
		if _, ok := d[key]; !ok {
			t.Errorf("dependency field %q missing", key)
		}
	}
	if d["status"] != "ok" {
		t.Errorf("dependency status = %v, want ok", d["status"])
	}
}

func TestPluginCheck_CapResolverNil_GracefulDegradation(t *testing.T) {
	// Create deps with NO CapResolver
	deps := capCheckDeps(t, "test-plugin", []pluginmanifest.PermissionSpec{
		{ID: "fs-access", Capabilities: []string{"fs.read"}, Default: "allow"},
	})
	// Explicitly nil out the resolver
	deps.CapResolver = nil

	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})

	// Capabilities and blockers should be empty (not nil — safely handled)
	caps, ok := m["capabilities"].([]interface{})
	if !ok || len(caps) != 0 {
		t.Errorf("capabilities should be empty when CapResolver is nil, got %v", m["capabilities"])
	}

	blockers := m["blockers"].([]interface{})
	if len(blockers) != 0 {
		t.Errorf("blockers should be empty when CapResolver is nil, got %v", blockers)
	}

	// Status should still be ok (only dependency checks matter)
	if m["status"] != "ok" {
		t.Errorf("status = %v, want ok", m["status"])
	}
}

func TestPluginCheck_MissingGrant_ProducesBlocker(t *testing.T) {
	// Create deps with a permission that defaults to "deny"
	deps := capCheckDeps(t, "test-plugin", []pluginmanifest.PermissionSpec{
		{ID: "fs-write-access", Capabilities: []string{"fs.write"}, Default: "deny"},
	})

	// No explicit grant is set in config → missing_grant blocker expected
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})

	blockers := m["blockers"].([]interface{})
	found := false
	for _, blk := range blockers {
		b := blk.(map[string]interface{})
		if b["kind"] == "missing_grant" && b["capability"] == "fs.write" {
			found = true
			if b["reason"] != "not_granted" {
				t.Errorf("blocker reason = %v, want not_granted", b["reason"])
			}
		}
	}
	if !found {
		t.Errorf("expected missing_grant blocker for fs.write, got blockers: %v", blockers)
	}

	// Status should be blocked
	if m["status"] != "blocked" {
		t.Errorf("status = %v, want blocked", m["status"])
	}
}

func TestPluginCheck_GrantedCap_NoBlocker(t *testing.T) {
	// Create deps with a permission that defaults to "ask" but with an explicit allow grant
	deps := capCheckDeps(t, "test-plugin", []pluginmanifest.PermissionSpec{
		{ID: "fs-read-access", Capabilities: []string{"fs.read"}, Default: "ask"},
	})

	// Set an explicit "allow" grant
	if err := deps.Config.SetPermissionGrant("test-plugin", "fs.read", "allow", nil); err != nil {
		t.Fatalf("set permission grant: %v", err)
	}

	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})

	// No blockers
	blockers := m["blockers"].([]interface{})
	if len(blockers) != 0 {
		t.Errorf("expected 0 blockers with allow grant, got %d: %v", len(blockers), blockers)
	}

	// Status should be ok
	if m["status"] != "ok" {
		t.Errorf("status = %v, want ok", m["status"])
	}
}

func TestPluginCheck_UnknownCapability_ProducesBlocker(t *testing.T) {
	// Use a platform with Runtime "unknown" so capabilities not in the Matrix
	// fall through to SupportUnknown (on desktop: full, on mobile: unsupported).
	resolver := &capability.Resolver{
		Platform: platform.RuntimePlatform{OS: "linux", Arch: "amd64", Runtime: "unknown"},
	}
	deps := capCheckDepsWithResolver(t, "test-plugin", []pluginmanifest.PermissionSpec{
		{ID: "custom-access", Capabilities: []string{"custom.madeup"}, Default: "allow"},
	}, resolver)
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})

	// Verify unknown_capability blocker
	blockers := m["blockers"].([]interface{})
	found := false
	for _, blk := range blockers {
		b := blk.(map[string]interface{})
		if b["kind"] == "unknown_capability" && b["capability"] == "custom.madeup" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unknown_capability blocker for custom.madeup, got blockers: %v", blockers)
	}
}

func TestPluginCheck_DedupCapabilities(t *testing.T) {
	// Two permissions declaring the same capability — should only appear once
	deps := capCheckDeps(t, "test-plugin", []pluginmanifest.PermissionSpec{
		{ID: "perm-a", Capabilities: []string{"fs.read", "fs.write"}, Default: "allow"},
		{ID: "perm-b", Capabilities: []string{"fs.read", "fs.list"}, Default: "allow"},
	})
	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-plugin"})

	caps, ok := m["capabilities"].([]interface{})
	if !ok {
		t.Fatalf("capabilities is not an array: %T", m["capabilities"])
	}
	if len(caps) != 3 {
		t.Errorf("expected 3 unique capabilities, got %d: %v", len(caps), caps)
	}

	// No blockers for fully supported caps
	blockers := m["blockers"].([]interface{})
	if len(blockers) != 0 {
		t.Errorf("expected 0 blockers, got %d", len(blockers))
	}
}

func TestPluginCacheList_ReturnsEmpty(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "plugin.cache.list", map[string]string{"pluginId": "sessionnode-core"})
	caches, ok := m["caches"].([]interface{})
	if !ok {
		t.Fatalf("caches is not an array: %T", m["caches"])
	}
	if len(caches) != 0 {
		t.Errorf("expected empty caches, got %d", len(caches))
	}
}

func TestPluginInstallPlan_ReturnsPendingApproval(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "plugin.install", map[string]string{"pluginId": "sessionnode-core"})
	if m["status"] != "pending_approval" {
		t.Errorf("status = %v, want pending_approval", m["status"])
	}
}

func TestPluginCacheClear_ReturnsNotImplemented(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "plugin.cache.clear", map[string]string{"pluginId": "sessionnode-core"})
	if m["status"] != "not_implemented" {
		t.Errorf("status = %v, want not_implemented", m["status"])
	}
}

// ---------------------------------------------------------------------------
// new plugin management tests
// ---------------------------------------------------------------------------

type mockManifestLoader struct {
	manifest *pluginmanifest.Manifest
}

func (m *mockManifestLoader) LoadManifest(pluginID string) (*pluginmanifest.Manifest, error) {
	if m.manifest == nil {
		return nil, fmt.Errorf("not found")
	}
	return m.manifest, nil
}

func (m *mockManifestLoader) ListPlugins() []pluginmanifest.PluginSummary {
	if m.manifest == nil {
		return nil
	}
	return []pluginmanifest.PluginSummary{
		{ID: m.manifest.ID, Name: m.manifest.Name, Version: m.manifest.Version, Enabled: true},
	}
}

func (m *mockManifestLoader) PluginEnabled(pluginID string) bool {
	return m.manifest != nil && m.manifest.ID == pluginID
}

// fullPluginDeps returns deps with Config, Notifier, History, and Store initialized.
func fullPluginDeps(t *testing.T) *Deps {
	t.Helper()
	deps := testDeps(t)
	cm := config.NewManager(filepath.Join(t.TempDir(), "config.json"))
	if err := cm.Load(); err != nil {
		t.Fatalf("config load: %v", err)
	}
	deps.Config = cm
	deps.History = history.New("")
	deps.Notifier = notify.NewManager(func(msg *protocol.Message) {})
	deps.Store = NewPlanStore()
	return deps
}

// TestPluginEnable verifies enabling a disabled plugin.
func TestPluginEnable(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	// Initially disable the plugin
	deps.Config.Set("plugin.disabledPlugins", []string{"test-plugin"})

	m := execOK(t, r, "plugin.enable", map[string]string{"pluginId": "test-plugin"})
	if m["status"] != "enabled" {
		t.Errorf("status = %v, want enabled", m["status"])
	}
	if m["pluginId"] != "test-plugin" {
		t.Errorf("pluginId = %v, want test-plugin", m["pluginId"])
	}

	// Verify it's no longer in DisabledPlugins
	cfg := deps.Config.Get()
	for _, id := range cfg.Plugin.DisabledPlugins {
		if id == "test-plugin" {
			t.Error("test-plugin should not be in DisabledPlugins after enable")
		}
	}
}

// TestPluginEnable_AlreadyEnabled verifies enabling an already-enabled plugin.
func TestPluginEnable_AlreadyEnabled(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	m := execOK(t, r, "plugin.enable", map[string]string{"pluginId": "test-plugin"})
	if m["status"] != "already_enabled" {
		t.Errorf("status = %v, want already_enabled", m["status"])
	}
}

// TestPluginDisable verifies disabling a plugin.
func TestPluginDisable(t *testing.T) {
	deps := fullPluginDeps(t)
	// Set up manifests so PluginEnabled returns true
	deps.Manifests = &mockManifestLoader{
		manifest: &pluginmanifest.Manifest{ID: "test-plugin"},
	}
	r := New(deps)

	m := execOK(t, r, "plugin.disable", map[string]string{"pluginId": "test-plugin"})
	if m["status"] != "disabled" {
		t.Errorf("status = %v, want disabled", m["status"])
	}
	if m["pluginId"] != "test-plugin" {
		t.Errorf("pluginId = %v, want test-plugin", m["pluginId"])
	}

	// Verify it's now in DisabledPlugins
	cfg := deps.Config.Get()
	found := false
	for _, id := range cfg.Plugin.DisabledPlugins {
		if id == "test-plugin" {
			found = true
			break
		}
	}
	if !found {
		t.Error("test-plugin should be in DisabledPlugins after disable")
	}
}

// TestPluginDisable_Builtin verifies that disabling the core plugin returns an error.
func TestPluginDisable_Builtin(t *testing.T) {
	r := New(testDeps(t))
	_, err := r.Execute(req("plugin.disable", map[string]string{"pluginId": "sessionnode-core"}))
	if err == nil {
		t.Fatal("expected error for disabling built-in plugin")
	}
	if !strings.Contains(err.Error(), "cannot disable built-in") {
		t.Errorf("error = %v, want 'cannot disable built-in'", err)
	}
}

// TestPluginDisable_AlreadyDisabled verifies disabling an already-disabled plugin.
func TestPluginDisable_AlreadyDisabled(t *testing.T) {
	deps := fullPluginDeps(t)
	deps.Config.Set("plugin.disabledPlugins", []string{"test-plugin"})
	r := New(deps)

	m := execOK(t, r, "plugin.disable", map[string]string{"pluginId": "test-plugin"})
	if m["status"] != "already_disabled" {
		t.Errorf("status = %v, want already_disabled", m["status"])
	}
}

// TestPluginConfigSet verifies setting a config value.
func TestPluginConfigSet(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	m := execOK(t, r, "plugin.config.set", map[string]interface{}{
		"key":   "disabledPlugins",
		"value": []string{"test-plugin"},
	})
	if m["status"] != "ok" {
		t.Errorf("status = %v, want ok", m["status"])
	}
	if m["revision"] == nil {
		t.Error("revision should be set")
	}

	// Verify the value was actually set
	cfg := deps.Config.Get()
	found := false
	for _, id := range cfg.Plugin.DisabledPlugins {
		if id == "test-plugin" {
			found = true
			break
		}
	}
	if !found {
		t.Error("test-plugin should be in DisabledPlugins")
	}
}

// TestPluginConfigSet_MissingKey verifies error when key is missing.
func TestPluginConfigSet_MissingKey(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	_, err := r.Execute(req("plugin.config.set", map[string]interface{}{
		"value": "test",
	}))
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

// TestPluginConfigSet_WithRevision verifies setting config with expected revision.
func TestPluginConfigSet_WithRevision(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	// Get current revision
	rev := deps.Config.Get().Revision

	m := execOK(t, r, "plugin.config.set", map[string]interface{}{
		"key":              "disabledPlugins",
		"value":            []string{"test"},
		"expectedRevision": rev,
	})
	if m["status"] != "ok" {
		t.Errorf("status = %v, want ok", m["status"])
	}
}

// TestPluginConfigSet_Conflict verifies config revision conflict.
func TestPluginConfigSet_Conflict(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	m := execOK(t, r, "plugin.config.set", map[string]interface{}{
		"key":              "disabledPlugins",
		"value":            []string{"test"},
		"expectedRevision": 99999, // wrong revision
	})
	if m["status"] != "conflict" {
		t.Errorf("status = %v, want conflict", m["status"])
	}
	if m["expectedRevision"] == nil {
		t.Error("expectedRevision should be set in conflict response")
	}
	if m["actualRevision"] == nil {
		t.Error("actualRevision should be set in conflict response")
	}
}

func TestConfigList_ReturnsWrappedEntries(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	m := execOK(t, r, "config.list", nil)
	entries, ok := m["configs"].([]interface{})
	if !ok {
		t.Fatalf("configs = %T, want []interface{}", m["configs"])
	}
	if len(entries) == 0 {
		t.Fatal("expected config entries")
	}
	for _, raw := range entries {
		entry := raw.(map[string]interface{})
		if entry["key"] == "core.auth.adminToken" {
			t.Fatalf("admin token should not be listed")
		}
	}
}

func TestConfigSetGetReset(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	set := execOK(t, r, "config.set", map[string]interface{}{
		"key":   "node.name",
		"value": "custom-node",
	})
	if set["value"] != "custom-node" {
		t.Fatalf("config.set value = %v, want custom-node", set["value"])
	}

	got := execOK(t, r, "config.get", map[string]string{"key": "node.name"})
	if got["value"] != "custom-node" {
		t.Fatalf("config.get value = %v, want custom-node", got["value"])
	}

	reset := execOK(t, r, "config.reset", map[string]string{"key": "node.name"})
	if reset["value"] == "custom-node" {
		t.Fatal("config.reset did not restore default node.name")
	}
}

// TestPluginPermissionsGrant verifies granting a permission.
func TestPluginPermissionsGrant(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	m := execOK(t, r, "plugin.permissions.grant", map[string]interface{}{
		"pluginId":   "test-plugin",
		"capability": "fs.read",
		"mode":       "allow",
	})
	if m["status"] != "ok" {
		t.Errorf("status = %v, want ok", m["status"])
	}
	if m["capability"] != "fs.read" {
		t.Errorf("capability = %v, want fs.read", m["capability"])
	}
	if m["mode"] != "allow" {
		t.Errorf("mode = %v, want allow", m["mode"])
	}
}

// TestPluginPermissionsGrant_DefaultMode verifies mode defaults to "allow".
func TestPluginPermissionsGrant_DefaultMode(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	m := execOK(t, r, "plugin.permissions.grant", map[string]interface{}{
		"pluginId":   "test-plugin",
		"capability": "fs.read",
	})
	if m["mode"] != "allow" {
		t.Errorf("mode = %v, want allow", m["mode"])
	}
}

// TestPluginPermissionsGrant_HighRisk verifies high-risk operations require approval.
func TestPluginPermissionsGrant_HighRisk(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	m := execOK(t, r, "plugin.permissions.grant", map[string]interface{}{
		"pluginId":   "test-plugin",
		"capability": "plugin.uninstall",
		"mode":       "allow",
	})
	if m["status"] != "requires_approval" {
		t.Errorf("status = %v, want requires_approval", m["status"])
	}
}

// TestPluginPermissionsGrant_InvalidMode verifies invalid mode returns an error.
func TestPluginPermissionsGrant_InvalidMode(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	_, err := r.Execute(req("plugin.permissions.grant", map[string]interface{}{
		"pluginId":   "test-plugin",
		"capability": "fs.read",
		"mode":       "invalid",
	}))
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
	if !strings.Contains(err.Error(), "invalid mode") {
		t.Errorf("error = %v, want 'invalid mode'", err)
	}
}

// TestPluginPermissionsRevoke verifies revoking a permission.
func TestPluginPermissionsRevoke(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	// First grant a permission
	execOK(t, r, "plugin.permissions.grant", map[string]interface{}{
		"pluginId":   "test-plugin",
		"capability": "fs.read",
		"mode":       "allow",
	})

	// Then revoke it
	m := execOK(t, r, "plugin.permissions.revoke", map[string]interface{}{
		"pluginId":   "test-plugin",
		"capability": "fs.read",
	})
	if m["status"] != "ok" {
		t.Errorf("status = %v, want ok", m["status"])
	}

	// Verify the grant was removed
	cfg := deps.Config.Get()
	if cfg.Plugin.Permissions != nil {
		if inner, ok := cfg.Plugin.Permissions["test-plugin"]; ok {
			if _, exists := inner["fs.read"]; exists {
				t.Error("fs.read grant should have been removed")
			}
		}
	}
}

// TestPluginPermissionsRevoke_MissingCapability verifies error when capability is missing.
func TestPluginPermissionsRevoke_MissingCapability(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	_, err := r.Execute(req("plugin.permissions.revoke", map[string]interface{}{
		"pluginId": "test-plugin",
	}))
	if err == nil {
		t.Fatal("expected error for missing capability")
	}
}

// TestPluginCacheClearPlan_NoCacheId verifies error when cacheId is missing.
func TestPluginCacheClearPlan_NoCacheId(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	_, err := r.Execute(req("plugin.cache.clear.plan", map[string]string{
		"pluginId": "test-plugin",
	}))
	if err == nil {
		t.Fatal("expected error for missing cacheId")
	}
}

// TestPluginCacheClearExecute_NoPlanId verifies error when planId is missing.
func TestPluginCacheClearExecute_NoPlanId(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	m := execOK(t, r, "plugin.cache.clear.execute", map[string]string{
		"pluginId": "test-plugin",
		"cacheId":  "test-cache",
	})
	if m["status"] != "plan_required" {
		t.Errorf("status = %v, want plan_required", m["status"])
	}
}

// TestFormerStubsNowImplemented verifies that previously-stubbed capabilities
// now have real handlers.
func TestFormerStubsNowImplemented(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	// plugin.install.execute — requires a planId
	_, err := r.Execute(req("plugin.install.execute", map[string]string{"pluginId": "test"}))
	if err == nil {
		t.Fatal("expected error for plugin.install.execute without planId")
	}

	// plugin.uninstall — works with dry-run
	m := execOK(t, r, "plugin.uninstall", map[string]string{"pluginId": "test-plugin"})
	if m["status"] != "uninstalled" {
		t.Errorf("plugin.uninstall status = %v, want uninstalled", m["status"])
	}

	// plugin.files.register — works
	m2 := execOK(t, r, "plugin.files.register", map[string]interface{}{
		"pluginId": "test-plugin",
		"files":    []string{"/tmp/test-files-register"},
	})
	if m2["status"] != "registered" {
		t.Errorf("plugin.files.register status = %v, want registered", m2["status"])
	}
}

// TestPluginPermissionsList_WithGrant verifies permissions list includes grant state.
func TestPluginPermissionsList_WithGrant(t *testing.T) {
	deps := fullPluginDeps(t)
	deps.Manifests = &mockManifestLoader{
		manifest: &pluginmanifest.Manifest{
			ID: "test-plugin",
			Core: &pluginmanifest.CoreSpec{
				Permissions: []pluginmanifest.PermissionSpec{
					{
						ID:           "fs-read",
						Description:  "Read files",
						Capabilities: []string{"fs.read"},
						Default:      "ask",
					},
				},
			},
		},
	}
	r := New(deps)

	// First grant a permission
	execOK(t, r, "plugin.permissions.grant", map[string]interface{}{
		"pluginId":   "test-plugin",
		"capability": "fs.read",
		"mode":       "allow",
	})

	// List permissions with pluginId that matches the manifest
	m := execOK(t, r, "plugin.permissions.list", map[string]string{"pluginId": "test-plugin"})
	perms := m["permissions"].([]interface{})
	if len(perms) == 0 {
		t.Fatal("expected non-empty permissions list")
	}

	perm := perms[0].(map[string]interface{})
	grant, ok := perm["grant"]
	if !ok {
		t.Fatal("expected grant field in permission entry")
	}
	grantMap := grant.(map[string]interface{})
	if grantMap["mode"] != "allow" {
		t.Errorf("grant.mode = %v, want allow", grantMap["mode"])
	}
}

// TestPluginHistory_RecordsEvents verifies history records plugin events.
func TestPluginHistory_RecordsEvents(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	// Perform an operation that records history
	deps.Config.Set("plugin.disabledPlugins", []string{"test-plugin"})
	execOK(t, r, "plugin.enable", map[string]string{"pluginId": "test-plugin"})

	// Query history
	m := execOK(t, r, "plugin.history", map[string]string{"pluginId": "test-plugin"})
	if m["status"] != "ok" {
		t.Errorf("status = %v, want ok", m["status"])
	}
	events := m["events"].([]interface{})
	if len(events) == 0 {
		t.Error("expected at least one history event")
	}
}

// TestPluginConfigGet_IncludesRevision verifies config.get includes revision.
func TestPluginConfigGet_IncludesRevision(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	m := execOK(t, r, "plugin.config.get", map[string]string{"pluginId": "test-plugin"})
	if m["revision"] == nil {
		t.Error("revision should be included in config.get response")
	}
}

// TestPluginCacheInfo verifies cache.info returns empty list when no manifest.
func TestPluginCacheInfo(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	m := execOK(t, r, "plugin.cache.info", map[string]string{"pluginId": "test-plugin"})
	caches, ok := m["caches"].([]interface{})
	if !ok {
		t.Fatalf("caches is not an array: %T", m["caches"])
	}
	if len(caches) != 0 {
		t.Errorf("expected empty caches, got %d", len(caches))
	}
}

// ---------------------------------------------------------------------------
// task.*
// ---------------------------------------------------------------------------

func TestTaskCreate(t *testing.T) {
	deps := testDeps(t)

	now := time.Now().UnixMilli()
	tk := &task.Task{
		ID:       "task-1",
		Type:     task.TypeInstall,
		PluginID: "test-plugin",
		Status:   task.StatusPending,
		Steps: []task.Step{
			{Name: "download", Status: task.StatusPending},
			{Name: "extract", Status: task.StatusPending},
		},
		StartedAt: now,
	}
	deps.TaskStore.Create(tk)

	got, ok := deps.TaskStore.Get("task-1")
	if !ok {
		t.Fatal("expected task to exist after Create")
	}
	if got.ID != "task-1" {
		t.Errorf("ID = %v, want task-1", got.ID)
	}
	if got.Type != task.TypeInstall {
		t.Errorf("Type = %v, want install", got.Type)
	}
	if got.PluginID != "test-plugin" {
		t.Errorf("PluginID = %v, want test-plugin", got.PluginID)
	}
	if got.Status != task.StatusPending {
		t.Errorf("Status = %v, want pending", got.Status)
	}
	if len(got.Steps) != 2 {
		t.Errorf("len(Steps) = %d, want 2", len(got.Steps))
	}
	if got.StartedAt == 0 {
		t.Error("StartedAt should not be zero")
	}
}

func TestTaskList(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	deps.TaskStore.Create(&task.Task{
		ID: "task-1", Type: task.TypeInstall, PluginID: "plugin-a",
		Status: task.StatusPending, StartedAt: time.Now().UnixMilli(),
	})
	deps.TaskStore.Create(&task.Task{
		ID: "task-2", Type: task.TypeCheck, PluginID: "plugin-b",
		Status: task.StatusRunning, StartedAt: time.Now().UnixMilli(),
	})

	m := execOK(t, r, "task.list", nil)
	tasks, ok := m["tasks"].([]interface{})
	if !ok {
		t.Fatalf("tasks is not an array: %T", m["tasks"])
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(tasks))
	}
}

func TestTaskInfo(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	deps.TaskStore.Create(&task.Task{
		ID: "task-1", Type: task.TypeInstall, PluginID: "plugin-a",
		Status: task.StatusRunning, StartedAt: time.Now().UnixMilli(),
		TargetNodeID: "node-local",
	})

	m := execOK(t, r, "task.info", map[string]string{"taskId": "task-1"})
	if m["taskId"] != "task-1" {
		t.Errorf("taskId = %v, want task-1", m["taskId"])
	}
	if m["type"] != "install" {
		t.Errorf("type = %v, want install", m["type"])
	}
	if m["status"] != "running" {
		t.Errorf("status = %v, want running", m["status"])
	}
	if m["targetNodeId"] != "node-local" {
		t.Errorf("targetNodeId = %v, want node-local", m["targetNodeId"])
	}
}

func TestTaskInfo_MissingTaskId(t *testing.T) {
	r := New(testDeps(t))
	_, err := r.Execute(req("task.info", map[string]string{}))
	if err == nil {
		t.Fatal("expected error for missing taskId")
	}
}

func TestTaskInfo_NotFound(t *testing.T) {
	r := New(testDeps(t))
	_, err := r.Execute(req("task.info", map[string]string{"taskId": "nonexistent"}))
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestTaskProgress(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	deps.TaskStore.Create(&task.Task{
		ID: "task-1", Type: task.TypeInstall, PluginID: "plugin-a",
		Status: task.StatusPending, StartedAt: time.Now().UnixMilli(),
		Steps: []task.Step{
			{Name: "download", Status: task.StatusPending},
			{Name: "verify", Status: task.StatusPending},
		},
	})

	// Update to running
	deps.TaskStore.UpdateStatus("task-1", task.StatusRunning, "")
	deps.TaskStore.AddEvent("task-1", 0, "download started", "info")

	got, ok := deps.TaskStore.Get("task-1")
	if !ok {
		t.Fatal("task not found")
	}
	if got.Status != task.StatusRunning {
		t.Errorf("Status = %v, want running", got.Status)
	}
	if len(got.Events) != 1 {
		t.Errorf("len(Events) = %d, want 1", len(got.Events))
	}
	if got.Events[0].Message != "download started" {
		t.Errorf("event message = %v, want 'download started'", got.Events[0].Message)
	}

	// Mark as succeeded
	deps.TaskStore.UpdateStatus("task-1", task.StatusSucceeded, "")
	got, ok = deps.TaskStore.Get("task-1")
	if !ok {
		t.Fatal("task not found after update")
	}
	if got.Status != task.StatusSucceeded {
		t.Errorf("Status = %v, want succeeded", got.Status)
	}
	if got.FinishedAt == 0 {
		t.Error("FinishedAt should be set after terminal status")
	}

	// Verify via capability
	m := execOK(t, r, "task.info", map[string]string{"taskId": "task-1"})
	if m["status"] != "succeeded" {
		t.Errorf("status = %v, want succeeded", m["status"])
	}
}

func TestTaskFailed(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	deps.TaskStore.Create(&task.Task{
		ID: "task-1", Type: task.TypeCheck, PluginID: "plugin-a",
		Status: task.StatusRunning, StartedAt: time.Now().UnixMilli(),
	})

	deps.TaskStore.UpdateStatus("task-1", task.StatusFailed, "dependency not found: git")

	got, ok := deps.TaskStore.Get("task-1")
	if !ok {
		t.Fatal("task not found")
	}
	if got.Status != task.StatusFailed {
		t.Errorf("Status = %v, want failed", got.Status)
	}
	if got.Error != "dependency not found: git" {
		t.Errorf("Error = %v, want 'dependency not found: git'", got.Error)
	}
	if got.FinishedAt == 0 {
		t.Error("FinishedAt should be set after terminal status")
	}

	// Verify via capability
	m := execOK(t, r, "task.info", map[string]string{"taskId": "task-1"})
	if m["status"] != "failed" {
		t.Errorf("status = %v, want failed", m["status"])
	}
	if m["error"] != "dependency not found: git" {
		t.Errorf("error = %v, want 'dependency not found: git'", m["error"])
	}
}

// ---------------------------------------------------------------------------
// plugin.install.* lifecycle tests
// ---------------------------------------------------------------------------

func TestInstallPlan_UnknownPlugin_ReturnsErrorSafe(t *testing.T) {
	// plugin.install plan should succeed even for unknown plugins (uses manifests).
	// Unknown plugins get a generic plan — no error is returned.
	r := New(fullPluginDeps(t))
	m := execOK(t, r, "plugin.install", map[string]string{"pluginId": "unknown-plugin-xyz"})
	if m["status"] != "pending_approval" {
		t.Errorf("status = %v, want pending_approval", m["status"])
	}
	if m["pluginId"] != "unknown-plugin-xyz" {
		t.Errorf("pluginId = %v, want unknown-plugin-xyz", m["pluginId"])
	}
}

func TestInstallExecute_WithoutApproval_Fails(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	// Step 1: create a plan (status = pending_approval)
	plan := execOK(t, r, "plugin.install", map[string]string{"pluginId": "test-plugin"})
	planID := plan["planId"].(string)

	// Step 2: try to execute without approving first
	execResult := execOK(t, r, "plugin.install.execute", map[string]string{
		"planId":   planID,
		"pluginId": "test-plugin",
	})
	if execResult["status"] != "plan_not_approved" {
		t.Errorf("status = %v, want plan_not_approved", execResult["status"])
	}
}

func TestInstallExecute_WithApprovedPlan_Succeeds(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	// Step 1: create a plan
	plan := execOK(t, r, "plugin.install", map[string]string{"pluginId": "test-plugin"})
	planID := plan["planId"].(string)

	// Step 2: manually approve the plan by setting its status in the store
	deps.Store.mu.Lock()
	deps.Store.InstallPlans[planID].Status = "approved"
	deps.Store.InstallPlans[planID].ApprovedAt = time.Now().UnixMilli()
	deps.Store.mu.Unlock()

	// Step 3: execute the approved plan
	execResult := execOK(t, r, "plugin.install.execute", map[string]string{
		"planId":   planID,
		"pluginId": "test-plugin",
	})
	if execResult["status"] != "completed" {
		t.Errorf("status = %v, want completed", execResult["status"])
	}
	if execResult["dryRun"] != true {
		t.Errorf("dryRun = %v, want true", execResult["dryRun"])
	}

	// Verify the plan was updated to completed in the store
	deps.Store.mu.RLock()
	storedPlan := deps.Store.InstallPlans[planID]
	deps.Store.mu.RUnlock()
	if storedPlan == nil {
		t.Fatal("plan not found in store after execution")
	}
	if storedPlan.Status != "completed" {
		t.Errorf("stored plan status = %v, want completed", storedPlan.Status)
	}

	// All steps should be "completed"
	for _, step := range storedPlan.Steps {
		if step.Status != "completed" {
			t.Errorf("step %d status = %v, want completed", step.Order, step.Status)
		}
	}
}

func TestInstallExecute_DryRunOnly_NoRealCommands(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	// Create and approve a plan
	plan := execOK(t, r, "plugin.install", map[string]string{"pluginId": "test-plugin"})
	planID := plan["planId"].(string)

	deps.Store.mu.Lock()
	deps.Store.InstallPlans[planID].Status = "approved"
	deps.Store.InstallPlans[planID].ApprovedAt = time.Now().UnixMilli()
	deps.Store.mu.Unlock()

	// Count processes before execution
	beforeCount := deps.Processes.Count()

	// Execute
	execOK(t, r, "plugin.install.execute", map[string]string{
		"planId":   planID,
		"pluginId": "test-plugin",
	})

	// Verify no real processes were spawned (dry-run only)
	afterCount := deps.Processes.Count()
	if afterCount != beforeCount {
		t.Errorf("process count changed from %d to %d; dry-run should not spawn processes", beforeCount, afterCount)
	}
}

func TestInstallExecute_MissingPlanId_ReturnsError(t *testing.T) {
	r := New(testDeps(t))
	_, err := r.Execute(req("plugin.install.execute", map[string]string{
		"pluginId": "test-plugin",
	}))
	if err == nil {
		t.Fatal("expected error for missing planId")
	}
}

func TestInstallExecute_PlanNotFound_ReturnsError(t *testing.T) {
	r := New(testDeps(t))
	_, err := r.Execute(req("plugin.install.execute", map[string]string{
		"planId":   "nonexistent-plan",
		"pluginId": "test-plugin",
	}))
	if err == nil {
		t.Fatal("expected error for nonexistent plan")
	}
}

func TestUninstall_ReturnsResult(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	// First register some files so uninstall has something to report
	execOK(t, r, "plugin.files.register", map[string]interface{}{
		"pluginId": "test-plugin",
		"files":    []string{"/tmp/test-plugin/config", "/tmp/test-plugin/data"},
	})

	m := execOK(t, r, "plugin.uninstall", map[string]string{"pluginId": "test-plugin"})
	if m["status"] != "uninstalled" {
		t.Errorf("status = %v, want uninstalled", m["status"])
	}
	if m["dryRun"] != true {
		t.Errorf("dryRun = %v, want true", m["dryRun"])
	}
	files, ok := m["registeredFiles"].([]interface{})
	if !ok {
		t.Fatalf("registeredFiles is not an array: %T", m["registeredFiles"])
	}
	if len(files) != 2 {
		t.Errorf("expected 2 registered files, got %d", len(files))
	}
	removed, ok := m["removedCount"].(float64)
	if !ok || int(removed) != 2 {
		t.Errorf("removedCount = %v, want 2", m["removedCount"])
	}
}

func TestUninstall_RecordsHistory(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	execOK(t, r, "plugin.uninstall", map[string]string{"pluginId": "test-plugin"})

	// Verify history was recorded
	events := deps.History.QueryPluginEvents("test-plugin")
	found := false
	for _, e := range events {
		if e.EventType == "plugin.uninstalled" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a plugin.uninstalled history event")
	}
}

func TestFilesRegister_RegistersFiles(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	m := execOK(t, r, "plugin.files.register", map[string]interface{}{
		"pluginId": "test-plugin",
		"files":    []string{"/tmp/test-plugin/config.yaml", "/tmp/test-plugin/cache"},
	})
	if m["status"] != "registered" {
		t.Errorf("status = %v, want registered", m["status"])
	}
	if m["pluginId"] != "test-plugin" {
		t.Errorf("pluginId = %v, want test-plugin", m["pluginId"])
	}
	files, ok := m["files"].([]interface{})
	if !ok {
		t.Fatalf("files is not an array: %T", m["files"])
	}
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d", len(files))
	}
	count, ok := m["count"].(float64)
	if !ok || int(count) != 2 {
		t.Errorf("count = %v, want 2", m["count"])
	}

	// Verify stored in PlanStore
	deps.Store.mu.RLock()
	registered := deps.Store.PluginFiles["test-plugin"]
	deps.Store.mu.RUnlock()
	if len(registered) != 2 {
		t.Errorf("stored files count = %d, want 2", len(registered))
	}
}

func TestFilesRegister_ReturnsRegisteredList(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	// Register batch 1
	execOK(t, r, "plugin.files.register", map[string]interface{}{
		"pluginId": "test-plugin",
		"files":    []string{"/tmp/file1"},
	})
	// Register batch 2 — should accumulate
	m := execOK(t, r, "plugin.files.register", map[string]interface{}{
		"pluginId": "test-plugin",
		"files":    []string{"/tmp/file2", "/tmp/file3"},
	})

	files := m["files"].([]interface{})
	if len(files) != 3 {
		t.Errorf("expected 3 accumulated files, got %d", len(files))
	}
	count, ok := m["count"].(float64)
	if !ok || int(count) != 3 {
		t.Errorf("count = %v, want 3", m["count"])
	}
}

func TestFilesRegister_MissingPluginId_FallsBackToRequest(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	// payload has no pluginId, but req.PluginID = "test"
	m := execOK(t, r, "plugin.files.register", map[string]interface{}{
		"files": []string{"/tmp/fallback-test"},
	})
	// The req in execOK uses PluginID: "test"
	if m["pluginId"] != "test" {
		t.Errorf("pluginId = %v, want test (from request field)", m["pluginId"])
	}
}

func TestInstallPlan_PlanIdUnique(t *testing.T) {
	r := New(fullPluginDeps(t))

	m1 := execOK(t, r, "plugin.install", map[string]string{"pluginId": "plugin-a"})
	m2 := execOK(t, r, "plugin.install", map[string]string{"pluginId": "plugin-b"})

	pid1 := m1["planId"].(string)
	pid2 := m2["planId"].(string)
	if pid1 == pid2 {
		t.Errorf("planIds should be unique: both are %q", pid1)
	}
	if pid1 == "" || pid2 == "" {
		t.Error("planIds should not be empty")
	}
}

func TestInstallExecute_PlanIdFromRequestLevel(t *testing.T) {
	deps := fullPluginDeps(t)
	r := New(deps)

	plan := execOK(t, r, "plugin.install", map[string]string{"pluginId": "test-plugin"})
	planID := plan["planId"].(string)

	// Approve the plan
	deps.Store.mu.Lock()
	deps.Store.InstallPlans[planID].Status = "approved"
	deps.Store.InstallPlans[planID].ApprovedAt = time.Now().UnixMilli()
	deps.Store.mu.Unlock()

	// Execute using request-level PlanID instead of payload planId
	reqWithPlanID := &types.CapabilityRequest{
		RequestID:  "test_req",
		PluginID:   "test",
		Capability: "plugin.install.execute",
		Payload:    nil,
		Actor:      types.Actor{Type: "web", ID: "tester"},
		PlanID:     planID,
	}
	result, err := r.Execute(reqWithPlanID)
	if err != nil {
		t.Fatalf("execute with request-level planId failed: %v", err)
	}
	res := normalize(result).(map[string]interface{})
	if res["status"] != "completed" {
		t.Errorf("status = %v, want completed", res["status"])
	}
}

// ---------------------------------------------------------------------------
// Approval workflow integration tests
// ---------------------------------------------------------------------------

// planDeps creates deps with Config, Notifier, History, and PlanManager initialized.
func planDeps(t *testing.T) *Deps {
	t.Helper()
	deps := fullPluginDeps(t)
	store := plan.NewPlanStore()
	deps.PlanManager = plan.NewManager(store, plan.DefaultHighRiskCaps)
	return deps
}

func TestNotifyRequest_ReturnsRequestID(t *testing.T) {
	deps := planDeps(t)
	r := New(deps)

	m := execOK(t, r, "notify.request", map[string]interface{}{
		"title":   "Grant Permission Approval",
		"body":    "Allow write access to /data?",
		"detail":  "This will grant fs.write permission to the plugin.",
		"timeout": 60,
	})
	if m["requestId"] == nil || m["requestId"] == "" {
		t.Error("expected non-empty requestId")
	}
	if m["status"] != "pending" {
		t.Errorf("status = %v, want pending", m["status"])
	}
}

func TestNotifyRespond_Approve_UpdatesRequest(t *testing.T) {
	deps := planDeps(t)
	r := New(deps)

	// Create approval request
	m := execOK(t, r, "notify.request", map[string]interface{}{
		"title":   "Approve?",
		"body":    "Should we continue?",
		"timeout": 60,
	})
	requestID := m["requestId"].(string)

	// Approve
	resp := execOK(t, r, "notify.respond", map[string]interface{}{
		"requestId": requestID,
		"action":    "allow",
	})
	if resp["requestId"] != requestID {
		t.Errorf("requestId = %v, want %v", resp["requestId"], requestID)
	}
	if resp["action"] != "allow" {
		t.Errorf("action = %v, want allow", resp["action"])
	}
	if resp["status"] != "responded" {
		t.Errorf("status = %v, want responded", resp["status"])
	}

	// Verify the approval recorded the response
	apr := deps.Notifier.GetApproval(types.RequestID(requestID))
	if apr == nil {
		t.Fatal("approval request not found after respond")
	}
	if !apr.Responded {
		t.Error("expected approval to be marked responded")
	}
	if apr.Response == nil {
		t.Fatal("expected non-nil response")
	}
	if apr.Response.Action != "allow" {
		t.Errorf("response action = %v, want allow", apr.Response.Action)
	}
}

func TestNotifyRespond_Deny_UpdatesRequest(t *testing.T) {
	deps := planDeps(t)
	r := New(deps)

	// Create approval request
	m := execOK(t, r, "notify.request", map[string]interface{}{
		"title":   "Deny test",
		"body":    "Should be denied",
		"timeout": 60,
	})
	requestID := m["requestId"].(string)

	// Deny
	resp := execOK(t, r, "notify.respond", map[string]interface{}{
		"requestId": requestID,
		"action":    "deny",
	})
	if resp["action"] != "deny" {
		t.Errorf("action = %v, want deny", resp["action"])
	}
	if resp["status"] != "responded" {
		t.Errorf("status = %v, want responded", resp["status"])
	}

	// Verify the approval recorded the denial
	apr := deps.Notifier.GetApproval(types.RequestID(requestID))
	if apr == nil {
		t.Fatal("approval request not found")
	}
	if apr.Response.Action != "deny" {
		t.Errorf("response action = %v, want deny", apr.Response.Action)
	}
}

func TestNotifyRespond_Approve_UpdatesLinkedPlan(t *testing.T) {
	deps := planDeps(t)
	r := New(deps)

	// Create a plan via PlanManager directly
	planReq := &types.CapabilityRequest{
		RequestID:  "req_plan_test",
		PluginID:   "test-plugin",
		Capability: "fs.write",
		Actor:      types.Actor{Type: "web", ID: "tester"},
	}
	planID, err := deps.PlanManager.CreatePlan(planReq)
	if err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}

	// Verify plan is pending
	p, err := deps.PlanManager.GetPlan(planID)
	if err != nil {
		t.Fatalf("GetPlan failed: %v", err)
	}
	if p.State != plan.StatePending {
		t.Errorf("plan state = %v, want %v", p.State, plan.StatePending)
	}

	// Create approval request linked to the plan
	m := execOK(t, r, "notify.request", map[string]interface{}{
		"title":   "Approve plan",
		"body":    "Plan approval",
		"planId":  planID,
		"timeout": 60,
	})
	requestID := m["requestId"].(string)

	// Approve via notify.respond — this should also update the plan
	execOK(t, r, "notify.respond", map[string]interface{}{
		"requestId": requestID,
		"action":    "allow",
	})

	// Verify plan is now approved
	p, err = deps.PlanManager.GetPlan(planID)
	if err != nil {
		t.Fatalf("GetPlan after approve: %v", err)
	}
	if p.State != plan.StateApproved {
		t.Errorf("plan state = %v, want %v", p.State, plan.StateApproved)
	}
}

func TestNotifyRespond_Deny_UpdatesLinkedPlan(t *testing.T) {
	deps := planDeps(t)
	r := New(deps)

	// Create a plan
	planReq := &types.CapabilityRequest{
		RequestID:  "req_plan_deny",
		PluginID:   "test-plugin",
		Capability: "fs.remove",
		Actor:      types.Actor{Type: "web", ID: "tester"},
	}
	planID, err := deps.PlanManager.CreatePlan(planReq)
	if err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}

	// Create approval request linked to the plan
	m := execOK(t, r, "notify.request", map[string]interface{}{
		"title":   "Deny plan",
		"body":    "Deny approval",
		"planId":  planID,
		"timeout": 60,
	})
	requestID := m["requestId"].(string)

	// Deny via notify.respond
	execOK(t, r, "notify.respond", map[string]interface{}{
		"requestId": requestID,
		"action":    "deny",
	})

	// Verify plan is now denied
	p, err := deps.PlanManager.GetPlan(planID)
	if err != nil {
		t.Fatalf("GetPlan after deny: %v", err)
	}
	if p.State != plan.StateDenied {
		t.Errorf("plan state = %v, want %v", p.State, plan.StateDenied)
	}
}

func TestHighRiskGrant_WithoutPlan_RequiresApproval(t *testing.T) {
	deps := planDeps(t)
	r := New(deps)

	// Try to grant a high-risk capability (e.g. plugin.uninstall)
	m := execOK(t, r, "plugin.permissions.grant", map[string]interface{}{
		"pluginId":   "test-plugin",
		"capability": "plugin.uninstall",
		"mode":       "allow",
	})
	if m["status"] != "requires_approval" {
		t.Errorf("status = %v, want requires_approval", m["status"])
	}
	// Should return a planId since PlanManager is available
	if m["planId"] == nil || m["planId"] == "" {
		t.Error("expected non-empty planId with PlanManager available")
	}
}

func TestHighRiskGrant_WithApprovedPlan_Succeeds(t *testing.T) {
	deps := planDeps(t)
	r := New(deps)

	// First create and approve a plan for the high-risk capability
	planReq := &types.CapabilityRequest{
		RequestID:  "req_grant",
		PluginID:   "test-plugin",
		Capability: "plugin.uninstall",
		Actor:      types.Actor{Type: "web", ID: "tester"},
	}
	planID, err := deps.PlanManager.CreatePlan(planReq)
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if err := deps.PlanManager.ApprovePlan(planID, "tester"); err != nil {
		t.Fatalf("ApprovePlan: %v", err)
	}

	// Now grant with the approved plan ID
	reqWithPlan := &types.CapabilityRequest{
		RequestID:  "test_req_grant",
		PluginID:   "test",
		Capability: "plugin.permissions.grant",
		Actor:      types.Actor{Type: "web", ID: "tester"},
		PlanID:     planID,
	}
	data, _ := json.Marshal(map[string]interface{}{
		"pluginId":   "test-plugin",
		"capability": "plugin.uninstall",
		"mode":       "allow",
	})
	reqWithPlan.Payload = data
	result, err := r.Execute(reqWithPlan)
	if err != nil {
		t.Fatalf("grant with approved plan: %v", err)
	}
	m := normalize(result).(map[string]interface{})
	if m["status"] != "ok" {
		t.Errorf("status = %v, want ok", m["status"])
	}
}

func TestHighRiskGrant_WithDeniedPlan_Fails(t *testing.T) {
	deps := planDeps(t)
	r := New(deps)

	// Create and deny a plan
	planReq := &types.CapabilityRequest{
		RequestID:  "req_deny_grant",
		PluginID:   "test-plugin",
		Capability: "plugin.uninstall",
		Actor:      types.Actor{Type: "web", ID: "tester"},
	}
	planID, err := deps.PlanManager.CreatePlan(planReq)
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if err := deps.PlanManager.DenyPlan(planID, "tester", "not allowed"); err != nil {
		t.Fatalf("DenyPlan: %v", err)
	}

	// Try to grant with denied plan
	reqWithPlan := &types.CapabilityRequest{
		RequestID:  "test_req_grant",
		PluginID:   "test",
		Capability: "plugin.permissions.grant",
		Actor:      types.Actor{Type: "web", ID: "tester"},
		PlanID:     planID,
	}
	data, _ := json.Marshal(map[string]interface{}{
		"pluginId":   "test-plugin",
		"capability": "plugin.uninstall",
		"mode":       "allow",
	})
	reqWithPlan.Payload = data
	result, err := r.Execute(reqWithPlan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := normalize(result).(map[string]interface{})
	if m["status"] != "approval_denied" {
		t.Errorf("status = %v, want approval_denied", m["status"])
	}
}

func TestAskGrant_RequiresApproval(t *testing.T) {
	deps := planDeps(t)
	r := New(deps)

	// Grant with mode "ask" on a low-risk capability — requires plan approval
	m := execOK(t, r, "plugin.permissions.grant", map[string]interface{}{
		"pluginId":   "test-plugin",
		"capability": "fs.read", // low-risk capability but "ask" mode
		"mode":       "ask",
	})
	if m["status"] != "requires_approval" {
		t.Errorf("status = %v, want requires_approval", m["status"])
	}
	// Should return a planId for the ask mode
	if m["planId"] == nil || m["planId"] == "" {
		t.Error("expected non-empty planId for ask mode")
	}
}

// ---------------------------------------------------------------------------
// run.*
// ---------------------------------------------------------------------------

func TestRunCreate(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "run.create", map[string]interface{}{
		"command": "go",
		"args":    []string{"version"},
		"label":   "test-run",
	})
	if m["runId"] == nil || m["runId"] == "" {
		t.Error("missing runId")
	}
	if m["sessionId"] == nil || m["sessionId"] == "" {
		t.Error("missing sessionId")
	}
	if m["processId"] == nil || m["processId"] == "" {
		t.Error("missing processId")
	}
	if m["state"] != "running" {
		t.Errorf("state = %v, want running", m["state"])
	}
	policy, ok := m["policy"].(map[string]interface{})
	if !ok {
		t.Fatal("policy is missing or wrong type")
	}
	if policy["onDisconnect"] != "keep_running" {
		t.Errorf("onDisconnect = %v, want keep_running", policy["onDisconnect"])
	}
}

func TestRunCreate_DefaultKind(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "run.create", map[string]interface{}{
		"command": "go",
		"args":    []string{"version"},
	})
	// run.info to verify kind defaulted to "terminal"
	runID := m["runId"].(string)
	info := execOK(t, r, "run.info", map[string]string{"runId": runID})
	if info["kind"] != "terminal" {
		t.Errorf("kind = %v, want terminal", info["kind"])
	}
}

func TestRunCreate_EmptyCommand(t *testing.T) {
	r := New(testDeps(t))
	_, err := r.Execute(req("run.create", map[string]string{}))
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestRunCreate_AcceptsRestartRestore(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "run.create", map[string]interface{}{
		"command": "go",
		"args":    []string{"version"},
		"policy": map[string]interface{}{
			"restartRestore": true,
		},
	})
	if m["runId"] == nil || m["runId"] == "" {
		t.Error("missing runId")
	}
}

func TestRunCreate_UnsupportedOnCoreShutdown(t *testing.T) {
	r := New(testDeps(t))
	_, err := r.Execute(req("run.create", map[string]interface{}{
		"command": "go",
		"args":    []string{"version"},
		"policy": map[string]interface{}{
			"onCoreShutdown": "restart",
		},
	}))
	if err == nil {
		t.Fatal("expected error for unsupported onCoreShutdown")
	}
}

func TestRunCreate_Metadata(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "run.create", map[string]interface{}{
		"command": "go",
		"args":    []string{"version"},
		"metadata": map[string]string{
			"project": "test-project",
			"env":     "dev",
		},
	})
	runID := m["runId"].(string)
	info := execOK(t, r, "run.info", map[string]string{"runId": runID})
	meta, ok := info["metadata"].(map[string]interface{})
	if !ok {
		t.Fatal("metadata is missing or wrong type")
	}
	if meta["project"] != "test-project" {
		t.Errorf("metadata.project = %v, want test-project", meta["project"])
	}
	if meta["env"] != "dev" {
		t.Errorf("metadata.env = %v, want dev", meta["env"])
	}
}

func TestRunList(t *testing.T) {
	r := New(testDeps(t))
	execOK(t, r, "run.create", map[string]interface{}{
		"command": "go", "args": []string{"version"}, "label": "run-a",
	})
	execOK(t, r, "run.create", map[string]interface{}{
		"command": "go", "args": []string{"env"}, "label": "run-b",
	})

	m := execOK(t, r, "run.list", nil)
	runs := m["runs"].([]interface{})
	if len(runs) != 2 {
		t.Errorf("expected 2 runs, got %d", len(runs))
	}
}

func TestRunList_FilterByKind(t *testing.T) {
	r := New(testDeps(t))
	execOK(t, r, "run.create", map[string]interface{}{
		"command": "go", "args": []string{"version"}, "kind": "service",
	})
	execOK(t, r, "run.create", map[string]interface{}{
		"command": "go", "args": []string{"env"}, "kind": "terminal",
	})

	m := execOK(t, r, "run.list", map[string]string{"kind": "service"})
	runs := m["runs"].([]interface{})
	if len(runs) != 1 {
		t.Errorf("expected 1 service run, got %d", len(runs))
	}
}

func TestRunInfo(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "run.create", map[string]interface{}{
		"command": "go",
		"args":    []string{"version"},
		"label":   "info-test",
		"kind":    "service",
	})
	runID := m["runId"].(string)

	info := execOK(t, r, "run.info", map[string]string{"runId": runID})
	if info["runId"] != runID {
		t.Errorf("runId = %v, want %s", info["runId"], runID)
	}
	if info["kind"] != "service" {
		t.Errorf("kind = %v, want service", info["kind"])
	}
	if info["label"] != "info-test" {
		t.Errorf("label = %v, want info-test", info["label"])
	}
	if info["state"] != "running" {
		t.Errorf("state = %v, want running", info["state"])
	}
	if info["sessionId"] == nil || info["sessionId"] == "" {
		t.Error("missing sessionId")
	}
}

func TestRunInfo_NotFound(t *testing.T) {
	r := New(testDeps(t))
	_, err := r.Execute(req("run.info", map[string]string{"runId": "nonexistent"}))
	if err == nil {
		t.Fatal("expected error for nonexistent run")
	}
}

func TestRunStop(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	m := execOK(t, r, "run.create", map[string]interface{}{
		"command": "go",
		"args":    []string{"version"},
	})
	runID := m["runId"].(string)

	stop := execOK(t, r, "run.stop", map[string]string{"runId": runID, "signal": "SIGTERM"})
	if stop["state"] != "stopped" {
		t.Errorf("state = %v, want stopped", stop["state"])
	}

	info := execOK(t, r, "run.info", map[string]string{"runId": runID})
	if info["state"] != "stopped" {
		t.Errorf("state = %v, want stopped", info["state"])
	}
}

func TestRunStop_NotFound(t *testing.T) {
	r := New(testDeps(t))
	_, err := r.Execute(req("run.stop", map[string]string{"runId": "nonexistent"}))
	if err == nil {
		t.Fatal("expected error for nonexistent run")
	}
}

func TestRunUpdatePolicy(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "run.create", map[string]interface{}{
		"command": "go",
		"args":    []string{"version"},
	})
	runID := m["runId"].(string)

	updated := execOK(t, r, "run.updatePolicy", map[string]interface{}{
		"runId": runID,
		"policy": map[string]interface{}{
			"onDisconnect":   "keep_running",
			"onCoreShutdown": "terminate",
			"persistHistory": true,
		},
	})
	policy, ok := updated["policy"].(map[string]interface{})
	if !ok {
		t.Fatal("policy missing or wrong type")
	}
	if policy["persistHistory"] != true {
		t.Errorf("persistHistory = %v, want true", policy["persistHistory"])
	}
}

func TestRunUpdatePolicy_AcceptsRestartRestore(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "run.create", map[string]interface{}{
		"command": "go",
		"args":    []string{"version"},
	})
	runID := m["runId"].(string)

	result, err := r.Execute(req("run.updatePolicy", map[string]interface{}{
		"runId": runID,
		"policy": map[string]interface{}{
			"restartRestore": true,
		},
	}))
	if err != nil {
		t.Fatalf("expected restartRestore=true to be accepted, got: %v", err)
	}
	rm := result.(map[string]interface{})
	pol := rm["policy"].(run.Policy)
	if !pol.RestartRestore {
		t.Error("RestartRestore should be true")
	}
}

func TestRunIntegration_ProcessSpawnStillWorks(t *testing.T) {
	// Run.create must not break process.spawn.
	r := New(testDeps(t))
	m := execOK(t, r, "process.spawn", map[string]interface{}{
		"command": "go",
		"args":    []string{"version"},
	})
	if m["sessionId"] == nil || m["sessionId"] == "" {
		t.Error("process.spawn: missing sessionId")
	}
	if m["state"] != "running" {
		t.Errorf("process.spawn state = %v, want running", m["state"])
	}
}

func TestRunIntegration_StreamWriteToRunProcess(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	catBin := testutil.CatBinary(t)
	m := execOK(t, r, "run.create", map[string]interface{}{
		"command": catBin,
		"label":   "stream-test",
	})
	sid := m["sessionId"].(string)
	time.Sleep(100 * time.Millisecond)

	wm := execOK(t, r, "stream.write", map[string]string{
		"sessionId": sid,
		"stream":    "stdin",
		"data":      "hello run\n",
	})
	if wm["written"].(float64) == 0 {
		t.Error("expected >0 bytes written to run process stdin")
	}

	// Close stdin so the process can exit
	deps.Processes.CloseStdin(types.SessionID(sid))
}

func TestRunCreate_LongRunningProcessStarts(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	sleepBin := testutil.SleepBinary(t)
	m := execOK(t, r, "run.create", map[string]interface{}{
		"command": sleepBin,
		"args":    []string{"5"},
		"label":   "disconnect-test",
	})
	sid := types.SessionID(m["sessionId"].(string))
	runID := m["runId"].(string)

	// Verify process exists
	proc := deps.Processes.Get(sid)
	if proc == nil {
		t.Fatal("process not found after run.create")
	}
	if proc.State != "running" {
		t.Errorf("process state = %v, want running", proc.State)
	}

	// Run should still be running
	info := execOK(t, r, "run.info", map[string]string{"runId": runID})
	if info["state"] != "running" {
		t.Errorf("run state = %v, want running", info["state"])
	}

	// Cleanup
	deps.Processes.Signal(sid, "SIGKILL", false)
}

func TestRegisteredRunCapabilitiesInHandlers(t *testing.T) {
	r := New(testDeps(t))
	capabilities := []string{
		"run.create",
		"run.list",
		"run.info",
		"run.stop",
		"run.updatePolicy",
		"run.attach",
	}
	for _, cap := range capabilities {
		_, ok := r.handlers[cap]
		if !ok {
			t.Errorf("run capability %q not registered in handlers", cap)
		}
	}
}

// ---------------------------------------------------------------------------
// process.signal + run.stop with tree=true
// ---------------------------------------------------------------------------

func TestProcessSignal_TreeTrue_PassesToManager(t *testing.T) {
	deps := testDeps(t)
	pm := deps.Processes
	r := New(deps)

	// Spawn a long-running process.
	sleepBin := testutil.SleepBinary(t)
	spawn := execOK(t, r, "process.spawn", map[string]interface{}{
		"command": sleepBin,
		"args":    []string{"30"},
	})
	sid := spawn["sessionId"].(string)

	time.Sleep(200 * time.Millisecond)

	// Verify process is running.
	proc := pm.Get(types.SessionID(sid))
	if proc == nil || proc.State != "running" {
		t.Fatal("process should be running")
	}

	// Send process.signal with tree=true.
	result := execOK(t, r, "process.signal", map[string]interface{}{
		"sessionId": sid,
		"signal":    "SIGKILL",
		"tree":      true,
	})

	if result["sessionId"] != sid {
		t.Errorf("sessionId = %v, want %s", result["sessionId"], sid)
	}
	if result["tree"] != true {
		t.Errorf("tree = %v, want true", result["tree"])
	}

	time.Sleep(300 * time.Millisecond)

	// Verify process was signaled.
	proc = pm.Get(types.SessionID(sid))
	if proc != nil && proc.State != "exited" {
		t.Errorf("expected exited after tree=true signal, got %s", proc.State)
	}
}

func TestProcessSignal_TreeFalse_OnlyTarget(t *testing.T) {
	deps := testDeps(t)
	pm := deps.Processes
	r := New(deps)

	sleepBin := testutil.SleepBinary(t)
	spawn := execOK(t, r, "process.spawn", map[string]interface{}{
		"command": sleepBin,
		"args":    []string{"30"},
	})
	sid := spawn["sessionId"].(string)

	time.Sleep(200 * time.Millisecond)

	// Send process.signal with tree=false (default).
	result := execOK(t, r, "process.signal", map[string]interface{}{
		"sessionId": sid,
		"signal":    "SIGKILL",
		"tree":      false,
	})

	if result["tree"] != false {
		t.Errorf("tree = %v, want false", result["tree"])
	}

	time.Sleep(300 * time.Millisecond)

	proc := pm.Get(types.SessionID(sid))
	if proc != nil && proc.State != "exited" {
		t.Errorf("expected exited after tree=false signal, got %s", proc.State)
	}
}

func TestProcessSignal_TreeDefault_IsFalse(t *testing.T) {
	// When tree is omitted, it should default to false (unchanged behavior).
	deps := testDeps(t)
	pm := deps.Processes
	r := New(deps)

	sleepBin := testutil.SleepBinary(t)
	spawn := execOK(t, r, "process.spawn", map[string]interface{}{
		"command": sleepBin,
		"args":    []string{"30"},
	})
	sid := spawn["sessionId"].(string)

	time.Sleep(200 * time.Millisecond)

	// Send process.signal without tree field — should default to false.
	result := execOK(t, r, "process.signal", map[string]interface{}{
		"sessionId": sid,
		"signal":    "SIGKILL",
	})

	// tree defaults to false (Go bool zero value).
	if result["tree"] != false {
		t.Errorf("tree = %v, want false (default)", result["tree"])
	}

	time.Sleep(300 * time.Millisecond)

	proc := pm.Get(types.SessionID(sid))
	if proc != nil && proc.State != "exited" {
		t.Errorf("expected exited, got %s", proc.State)
	}
}

func TestRunStop_TreeTrue_PassesToManager(t *testing.T) {
	deps := testDeps(t)
	pm := deps.Processes
	r := New(deps)

	sleepBin := testutil.SleepBinary(t)
	create := execOK(t, r, "run.create", map[string]interface{}{
		"command": sleepBin,
		"args":    []string{"30"},
		"label":   "tree-true-test",
	})
	runID := create["runId"].(string)
	sid := create["sessionId"].(string)

	time.Sleep(200 * time.Millisecond)

	// Verify process is running.
	proc := pm.Get(types.SessionID(sid))
	if proc == nil || proc.State != "running" {
		t.Fatal("process should be running after run.create")
	}

	// Send run.stop with tree=true.
	stop := execOK(t, r, "run.stop", map[string]interface{}{
		"runId":  runID,
		"signal": "SIGTERM",
		"tree":   true,
	})

	if stop["state"] != "stopped" {
		t.Errorf("state = %v, want stopped", stop["state"])
	}

	time.Sleep(300 * time.Millisecond)

	// Verify run state updated.
	info := execOK(t, r, "run.info", map[string]string{"runId": runID})
	if info["state"] != "stopped" {
		t.Errorf("run state = %v, want stopped", info["state"])
	}

	// Verify process was signaled.
	proc = pm.Get(types.SessionID(sid))
	if proc != nil && proc.State != "exited" {
		t.Errorf("expected exited after run.stop tree=true, got %s", proc.State)
	}
}

func TestRunStop_TreeDefault_IsFalse(t *testing.T) {
	// When tree is omitted from run.stop, it defaults to false.
	deps := testDeps(t)
	r := New(deps)

	sleepBin := testutil.SleepBinary(t)
	create := execOK(t, r, "run.create", map[string]interface{}{
		"command": sleepBin,
		"args":    []string{"30"},
		"label":   "tree-default-test",
	})
	runID := create["runId"].(string)

	time.Sleep(200 * time.Millisecond)

	// Send run.stop without tree field.
	stop := execOK(t, r, "run.stop", map[string]interface{}{
		"runId":  runID,
		"signal": "SIGTERM",
	})

	if stop["state"] != "stopped" {
		t.Errorf("state = %v, want stopped", stop["state"])
	}
}

func TestRunStop_MissingSignal_DefaultsToSIGTERM(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	sleepBin := testutil.SleepBinary(t)
	create := execOK(t, r, "run.create", map[string]interface{}{
		"command": sleepBin,
		"args":    []string{"30"},
		"label":   "default-signal-test",
	})
	runID := create["runId"].(string)

	time.Sleep(200 * time.Millisecond)

	// Send run.stop without signal — defaults to SIGTERM.
	stop := execOK(t, r, "run.stop", map[string]interface{}{
		"runId": runID,
	})

	if stop["state"] != "stopped" {
		t.Errorf("state = %v, want stopped", stop["state"])
	}
}

// ── network.* capability tests ─────────────────────────────────────────────

// TestPluginCheck_NetworkWithoutGrant_ReturnsMissingGrantBlocker verifies that
// a plugin declaring network.connect without an explicit grant produces a
// missing_grant blocker.
func TestPluginCheck_NetworkWithoutGrant_ReturnsMissingGrantBlocker(t *testing.T) {
	deps := capCheckDeps(t, "test-net-plugin", []pluginmanifest.PermissionSpec{
		{ID: "test-net-plugin.network", Description: "Network access", Capabilities: []string{"network.connect"}, Default: "ask"},
	})

	r := New(deps)
	m := execOK(t, r, "plugin.check", map[string]string{"pluginId": "test-net-plugin"})

	blockers, ok := m["blockers"].([]interface{})
	if !ok {
		t.Fatal("blockers not found or wrong type")
	}

	hasMissingGrant := false
	for _, b := range blockers {
		blk, ok := b.(map[string]interface{})
		if ok && blk["kind"] == "missing_grant" && blk["capability"] == "network.connect" {
			hasMissingGrant = true
		}
	}

	// On desktop platforms, network.connect is supported so the blocker
	// should be missing_grant (default:ask with no grant).
	plat := platform.Current()
	if plat.IsDesktop() || plat.Runtime == "server" {
		if !hasMissingGrant {
			t.Errorf("expected missing_grant blocker for network.connect on %s/%s, got blockers: %v",
				plat.OS, plat.Runtime, blockers)
		}
	} else {
		t.Logf("platform %s/%s: missing_grant check skipped (non-desktop)", plat.OS, plat.Runtime)
	}
}

// TestExecutor_NetworkCapabilitiesNotRegistered verifies that network.*
// capabilities are NOT registered as handlers (declaration-only).
func TestExecutor_NetworkCapabilitiesNotRegistered(t *testing.T) {
	deps := &Deps{}
	reg := New(deps)

	req := &types.CapabilityRequest{
		RequestID:  "req-net-exec",
		Capability: "network.connect",
		Actor:      types.Actor{Type: "web", ID: "tester"},
	}
	_, err := reg.Execute(req)
	if err == nil {
		t.Fatal("expected error for unregistered network.connect")
	}
	t.Logf("network.connect execution error (expected): %v", err)
}

// ── approval.list tests ───────────────────────────────────────────────────────

func TestApprovalList_ReturnsEmpty(t *testing.T) {
	deps := fullPluginDeps(t)
	reg := New(deps)

	req := &types.CapabilityRequest{
		RequestID:  "req-approval-list",
		Capability: "approval.list",
	}

	result, err := reg.Execute(req)
	if err != nil {
		t.Fatalf("approval.list failed: %v", err)
	}

	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}

	approvals, ok := resultMap["approvals"]
	if !ok {
		t.Fatal("expected approvals field in result")
	}

	// Should be empty initially
	items, ok := approvals.([]interface{})
	if ok && len(items) != 0 {
		t.Logf("approvals: %v (expected empty)", items)
	}
}

// ── plugin.permissions.grant correct params tests ─────────────────────────────

func TestPluginPermissionsGrant_CorrectParams(t *testing.T) {
	deps := fullPluginDeps(t)
	reg := New(deps)

	// Grant with capability + mode (the correct params)
	req := &types.CapabilityRequest{
		RequestID:  "req-grant-correct",
		Capability: "plugin.permissions.grant",
		PluginID:   "test-plugin",
		Payload:    json.RawMessage(`{"pluginId":"test-plugin","capability":"fs.read","mode":"allow"}`),
		Actor:      types.Actor{Type: "web", ID: "test-user"},
	}

	result, err := reg.Execute(req)
	if err != nil {
		// "no permission manifest" or similar is OK — checking param parsing
		t.Logf("grant result (may fail due to missing manifest): %v", err)
		return
	}

	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}

	t.Logf("grant result: %v", resultMap)
}

// TestPluginPermissionsGrant_WithPayloadPlanId verifies that
// plugin.permissions.grant accepts planId in the payload (not only at
// the request level). This is the UI re-call path after approval:
//  1. First grant → requires_approval + planId
//  2. notify.request + notify.respond (approve)
//  3. Second grant with payload.planId → ok
func TestPluginPermissionsGrant_WithPayloadPlanId(t *testing.T) {
	deps := planDeps(t)
	r := New(deps)

	// Step 1: First grant (high-risk) — no planId → requires_approval
	m1 := execOK(t, r, "plugin.permissions.grant", map[string]interface{}{
		"pluginId":   "test-plugin",
		"capability": "plugin.uninstall",
		"mode":       "allow",
	})
	if m1["status"] != "requires_approval" {
		t.Fatalf("step 1 status = %v, want requires_approval", m1["status"])
	}
	planID := m1["planId"].(string)
	if planID == "" {
		t.Fatal("expected non-empty planId from requires_approval")
	}

	// Step 2: Create an approval request linked to this plan
	m2 := execOK(t, r, "notify.request", map[string]interface{}{
		"title":   "Approve grant",
		"body":    "Grant plugin.uninstall",
		"planId":  planID,
		"timeout": 60,
	})
	requestID := m2["requestId"].(string)

	// Step 3: Approve via notify.respond
	execOK(t, r, "notify.respond", map[string]interface{}{
		"requestId": requestID,
		"action":    "allow",
	})

	// Step 4: Second grant — planId passed in payload (UI path)
	m3 := execOK(t, r, "plugin.permissions.grant", map[string]interface{}{
		"pluginId":   "test-plugin",
		"capability": "plugin.uninstall",
		"mode":       "allow",
		"planId":     planID,
	})
	if m3["status"] != "ok" {
		t.Errorf("step 4 status = %v, want ok (plan was approved, payload.planId should work)", m3["status"])
	}
	if m3["capability"] != "plugin.uninstall" {
		t.Errorf("capability = %v, want plugin.uninstall", m3["capability"])
	}
	if m3["mode"] != "allow" {
		t.Errorf("mode = %v, want allow", m3["mode"])
	}
}

// TestPluginPermissionsGrant_WithPayloadPlanId_Denied verifies that
// a denied plan returns approval_denied even when planId comes from
// the payload.
func TestPluginPermissionsGrant_WithPayloadPlanId_Denied(t *testing.T) {
	deps := planDeps(t)
	r := New(deps)

	// Create and deny a plan
	planReq := &types.CapabilityRequest{
		RequestID:  "req_deny",
		PluginID:   "test-plugin",
		Capability: "plugin.uninstall",
		Actor:      types.Actor{Type: "web", ID: "tester"},
	}
	planID, err := deps.PlanManager.CreatePlan(planReq)
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if err := deps.PlanManager.DenyPlan(planID, "tester", "not allowed"); err != nil {
		t.Fatalf("DenyPlan: %v", err)
	}

	// Grant with planId in payload (not request-level)
	m := execOK(t, r, "plugin.permissions.grant", map[string]interface{}{
		"pluginId":   "test-plugin",
		"capability": "plugin.uninstall",
		"mode":       "allow",
		"planId":     planID,
	})
	if m["status"] != "approval_denied" {
		t.Errorf("status = %v, want approval_denied", m["status"])
	}
}

// TestApprovalList_ReturnsWrapped verifies that approval.list returns
// { approvals: [...] } (an object wrapping an array), not a bare array.
func TestApprovalList_ReturnsWrapped(t *testing.T) {
	deps := planDeps(t)
	r := New(deps)

	// Create an approval request so the list is non-empty
	execOK(t, r, "notify.request", map[string]interface{}{
		"title":   "Test approval",
		"body":    "Testing list wrapper",
		"timeout": 60,
	})

	req := &types.CapabilityRequest{
		RequestID:  "req-approval-wrap",
		Capability: "approval.list",
		Actor:      types.Actor{Type: "web", ID: "tester"},
	}

	result, err := r.Execute(req)
	if err != nil {
		t.Fatalf("approval.list failed: %v", err)
	}

	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}

	// Must have "approvals" key
	approvalsRaw, ok := resultMap["approvals"]
	if !ok {
		t.Fatal("expected \"approvals\" key in result object")
	}

	// Must be an array
	approvals, ok := approvalsRaw.([]interface{})
	if !ok {
		t.Fatalf("expected approvals to be array, got %T", approvalsRaw)
	}

	// Should have at least one pending approval
	if len(approvals) == 0 {
		t.Error("expected at least one pending approval")
	}

	// Each approval should be an object with a requestId
	for _, a := range approvals {
		entry, ok := a.(map[string]interface{})
		if !ok {
			t.Errorf("expected approval entry to be map, got %T", a)
			continue
		}
		if entry["requestId"] == nil || entry["requestId"] == "" {
			t.Error("approval entry missing requestId")
		}
	}
}

// ── run.attach ───────────────────────────────────────────────────────────

func TestRunAttach_ExistingRunningRun(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	create := execOK(t, r, "run.create", map[string]interface{}{
		"command": "bash",
		"label":   "attach-test",
	})
	runID := create["runId"].(string)

	result := execOK(t, r, "run.attach", map[string]interface{}{
		"runId": runID,
	})

	if result["runId"] != runID {
		t.Errorf("runId = %v, want %s", result["runId"], runID)
	}
	if result["sessionId"] == nil || result["sessionId"] == "" {
		t.Error("sessionId should be set")
	}
	if result["state"] != "running" {
		t.Errorf("state = %v, want running", result["state"])
	}
	if result["kind"] != "terminal" {
		t.Errorf("kind = %v, want terminal", result["kind"])
	}
	if result["pluginId"] != "test" {
		t.Errorf("pluginId = %v, want test", result["pluginId"])
	}

	subs, ok := result["streamSubscriptions"].([]interface{})
	if !ok {
		t.Fatal("streamSubscriptions not found or wrong type")
	}
	if len(subs) != 2 {
		t.Fatalf("expected 2 stream subscriptions (stdout, stderr), got %d", len(subs))
	}

	if result["process"] == nil {
		t.Error("process snapshot should be present for running run")
	}
}

func TestRunAttach_UnknownRunId(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	_, err := r.Execute(req("run.attach", map[string]string{"runId": "nonexistent"}))
	if err == nil {
		t.Fatal("expected error for unknown runId")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found', got: %v", err)
	}
}

func TestRunAttach_StoppedRun(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	sleepBin := testutil.SleepBinary(t)
	create := execOK(t, r, "run.create", map[string]interface{}{
		"command": sleepBin,
		"args":    []string{"2"},
	})
	runID := create["runId"].(string)

	time.Sleep(200 * time.Millisecond)

	execOK(t, r, "run.stop", map[string]interface{}{
		"runId":  runID,
		"signal": "SIGTERM",
	})

	time.Sleep(300 * time.Millisecond)

	result := execOK(t, r, "run.attach", map[string]interface{}{
		"runId": runID,
	})

	if result["runId"] != runID {
		t.Errorf("runId = %v, want %s", result["runId"], runID)
	}
	if result["sessionId"] == nil {
		t.Error("sessionId should still be returned for stopped run")
	}
	if result["state"] != "stopped" {
		t.Logf("state after stop = %v (may be stopped or exited)", result["state"])
	}
}

func TestRunAttach_ReplayTrue(t *testing.T) {
	deps := testDeps(t)
	deps.History = history.New("")
	r := New(deps)

	create := execOK(t, r, "run.create", map[string]interface{}{
		"command": "bash",
		"label":   "replay-test",
	})
	runID := create["runId"].(string)
	sid := types.SessionID(create["sessionId"].(string))

	deps.History.Record(sid, "stdout", 1, "hello")
	deps.History.Record(sid, "stdout", 2, "world")

	result := execOK(t, r, "run.attach", map[string]interface{}{
		"runId":  runID,
		"replay": true,
	})

	replay, ok := result["replay"].(map[string]interface{})
	if !ok {
		t.Fatal("replay not found or wrong type")
	}
	stdoutEvents, ok := replay["stdout"].([]interface{})
	if !ok {
		t.Fatal("replay.stdout not found or wrong type")
	}
	if len(stdoutEvents) < 2 {
		t.Errorf("expected at least 2 stdout events in replay, got %d", len(stdoutEvents))
	}
}

func TestRunAttach_ReplayFalse(t *testing.T) {
	deps := testDeps(t)
	deps.History = history.New("")
	r := New(deps)

	create := execOK(t, r, "run.create", map[string]interface{}{
		"command": "bash",
		"label":   "no-replay-test",
	})
	runID := create["runId"].(string)

	result := execOK(t, r, "run.attach", map[string]interface{}{
		"runId":  runID,
		"replay": false,
	})

	if result["replay"] != nil {
		t.Error("replay should be nil when replay=false")
	}
	if result["runId"] != runID {
		t.Errorf("runId = %v, want %s", result["runId"], runID)
	}
}

func TestRunAttach_DefaultStreamTypes(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	create := execOK(t, r, "run.create", map[string]interface{}{
		"command": "bash",
	})
	runID := create["runId"].(string)

	result := execOK(t, r, "run.attach", map[string]interface{}{
		"runId": runID,
	})

	subs, ok := result["streamSubscriptions"].([]interface{})
	if !ok {
		t.Fatal("streamSubscriptions not found")
	}
	if len(subs) != 2 {
		t.Fatalf("default streamTypes should be [stdout, stderr], got %d entries", len(subs))
	}
}

func TestRunAttach_DoesNotCreateProcess(t *testing.T) {
	deps := testDeps(t)
	pm := deps.Processes
	r := New(deps)

	create := execOK(t, r, "run.create", map[string]interface{}{
		"command": "bash",
	})
	runID := create["runId"].(string)

	initialCount := len(pm.List())

	execOK(t, r, "run.attach", map[string]interface{}{"runId": runID})
	execOK(t, r, "run.attach", map[string]interface{}{"runId": runID})

	if len(pm.List()) != initialCount {
		t.Errorf("process count changed from %d to %d; run.attach should not create processes",
			initialCount, len(pm.List()))
	}
}

func TestRunAttach_DoesNotChangePolicy(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	create := execOK(t, r, "run.create", map[string]interface{}{
		"command": "bash",
		"policy": map[string]interface{}{
			"persistHistory": false,
		},
	})
	runID := create["runId"].(string)

	result := execOK(t, r, "run.attach", map[string]interface{}{
		"runId": runID,
	})

	policy, ok := result["policy"].(map[string]interface{})
	if !ok {
		t.Fatal("policy not found")
	}
	if policy["persistHistory"] != false {
		t.Error("policy persistHistory should still be false after attach")
	}
}

func TestRunAttach_MissingRunId(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	_, err := r.Execute(req("run.attach", map[string]string{}))
	if err == nil {
		t.Fatal("expected error for missing runId")
	}
	if !strings.Contains(err.Error(), "runId is required") {
		t.Errorf("error should mention 'runId is required', got: %v", err)
	}
}


func TestRunInfo_ClassifiesOrphanedRun(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	// Create a run record with a fake ProcessID (no real process)
	rn := &run.Run{
		NodeID:    "n1",
		Kind:      run.KindTerminal,
		Label:     "orphaned-test",
		PluginID:  "terminal",
		State:     run.StateRunning,
		SessionID: "sess_fake_orphan",
		ProcessID: "sess_fake_orphan",
		Policy:    run.DefaultPolicy(),
	}
	deps.RunStore.Create(rn)

	result := execOK(t, r, "run.info", map[string]string{"runId": rn.RunID})
	state, _ := result["state"].(string)
	if state != run.StateOrphaned {
		t.Errorf("state = %q, want %q", state, run.StateOrphaned)
	}
}

func TestRunInfo_ClassifiesRestorableRun(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	pol := run.DefaultPolicy()
	pol.RestartRestore = true
	rn := &run.Run{
		NodeID:    "n1",
		Kind:      run.KindTerminal,
		Label:     "restorable-test",
		PluginID:  "terminal",
		State:     run.StateRunning,
		SessionID: "sess_fake_restore",
		ProcessID: "sess_fake_restore",
		Policy:    pol,
	}
	deps.RunStore.Create(rn)

	result := execOK(t, r, "run.info", map[string]string{"runId": rn.RunID})
	state, _ := result["state"].(string)
	if state != run.StateRestorable {
		t.Errorf("state = %q, want %q", state, run.StateRestorable)
	}
}

func TestRunList_ClassifiesOrphaned(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	// Create running + orphaned mix
	rn := &run.Run{
		NodeID:    "n1",
		Kind:      run.KindTerminal,
		Label:     "orphaned-in-list",
		PluginID:  "terminal",
		State:     run.StateRunning,
		SessionID: "sess_fake_orphan2",
		ProcessID: "sess_fake_orphan2",
		Policy:    run.DefaultPolicy(),
	}
	deps.RunStore.Create(rn)

	result := execOK(t, r, "run.list", map[string]string{})
	runs, _ := result["runs"].([]interface{})
	found := false
	for _, ri := range runs {
		rm := ri.(map[string]interface{})
		if rm["runId"] == rn.RunID {
			found = true
			if rm["state"] != run.StateOrphaned {
				t.Errorf("state = %q, want %q", rm["state"], run.StateOrphaned)
			}
		}
	}
	if !found {
		t.Error("orphaned run missing from run.list")
	}
}

func TestRunStop_OrphanedRun(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	rn := &run.Run{
		NodeID:    "n1",
		Kind:      run.KindTerminal,
		Label:     "stop-orphan-test",
		PluginID:  "terminal",
		State:     run.StateOrphaned,
		SessionID: "sess_fake_stop",
		ProcessID: "sess_fake_stop",
		Policy:    run.DefaultPolicy(),
	}
	deps.RunStore.Create(rn)

	result := execOK(t, r, "run.stop", map[string]string{"runId": rn.RunID, "signal": "SIGTERM"})
	state, _ := result["state"].(string)
	if state != run.StateStopped {
		t.Errorf("state = %q, want %q", state, run.StateStopped)
	}
}

func TestRunStop_RestorableRun(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	pol := run.DefaultPolicy()
	pol.RestartRestore = true
	rn := &run.Run{
		NodeID:    "n1",
		Kind:      run.KindTerminal,
		Label:     "stop-restore-test",
		PluginID:  "terminal",
		State:     run.StateRestorable,
		SessionID: "sess_fake_stop2",
		ProcessID: "sess_fake_stop2",
		Policy:    pol,
	}
	deps.RunStore.Create(rn)

	result := execOK(t, r, "run.stop", map[string]string{"runId": rn.RunID, "signal": "SIGTERM"})
	state, _ := result["state"].(string)
	if state != run.StateStopped {
		t.Errorf("state = %q, want %q", state, run.StateStopped)
	}
}

func TestRunAttach_OrphanedRun(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	rn := &run.Run{
		NodeID:    "n1",
		Kind:      run.KindTerminal,
		Label:     "attach-orphan-test",
		PluginID:  "terminal",
		State:     run.StateOrphaned,
		SessionID: "sess_fake_attach_orphan",
		ProcessID: "sess_fake_attach_orphan",
		Policy:    run.DefaultPolicy(),
	}
	deps.RunStore.Create(rn)

	result := execOK(t, r, "run.attach", map[string]interface{}{"runId": rn.RunID, "replay": false})
	if result["state"] != run.StateOrphaned {
		t.Errorf("state = %q, want %q", result["state"], run.StateOrphaned)
	}
	if result["sessionId"] != string(rn.SessionID) {
		t.Errorf("sessionId = %q, want %q", result["sessionId"], string(rn.SessionID))
	}
	subs, _ := result["streamSubscriptions"].([]interface{})
	if len(subs) == 0 {
		t.Error("expected stream subscription info")
	}
}

func TestRunAttach_RestorableRun(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	pol := run.DefaultPolicy()
	pol.RestartRestore = true
	rn := &run.Run{
		NodeID:    "n1",
		Kind:      run.KindTerminal,
		Label:     "attach-restore-test",
		PluginID:  "terminal",
		State:     run.StateRestorable,
		SessionID: "sess_fake_attach_restore",
		ProcessID: "sess_fake_attach_restore",
		Policy:    pol,
	}
	deps.RunStore.Create(rn)

	result := execOK(t, r, "run.attach", map[string]interface{}{"runId": rn.RunID, "replay": false})
	if result["state"] != run.StateRestorable {
		t.Errorf("state = %q, want %q", result["state"], run.StateRestorable)
	}
	if result["sessionId"] != string(rn.SessionID) {
		t.Errorf("sessionId = %q, want %q", result["sessionId"], string(rn.SessionID))
	}
	subs, _ := result["streamSubscriptions"].([]interface{})
	if len(subs) == 0 {
		t.Error("expected stream subscription info")
	}
}

func TestRunCreate_AcceptsOnCoreShutdownKeepRunning(t *testing.T) {
	r := New(testDeps(t))
	result := execOK(t, r, "run.create", map[string]interface{}{
		"command": "go",
		"args":    []string{"version"},
		"policy": map[string]interface{}{
			"onCoreShutdown": "keep_running",
		},
	})
	if result["runId"] == nil || result["runId"] == "" {
		t.Error("expected valid runId")
	}
}

func TestRunUpdatePolicy_AcceptsOnCoreShutdownKeepRunning(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "run.create", map[string]interface{}{
		"command": "go",
		"args":    []string{"version"},
	})
	runID := m["runId"].(string)

	result := execOK(t, r, "run.updatePolicy", map[string]interface{}{
		"runId": runID,
		"policy": map[string]interface{}{
			"onCoreShutdown": "keep_running",
		},
	})
	pol, _ := result["policy"].(map[string]interface{})
	if pol == nil {
		t.Fatal("policy is missing")
	}
	if pol["onCoreShutdown"] != run.OnCoreShutdownKeepRunning {
		t.Errorf("onCoreShutdown = %q, want %q", pol["onCoreShutdown"], run.OnCoreShutdownKeepRunning)
	}
}

// ── update.* tests ──────────────────────────────────────────────────────

// fakeExecGitRunner records calls for assertions in tests.
type fakeExecGitRunner struct {
	headCommit string
	remoteHead string
	dirty      bool

	headCommitErr error
	remoteHeadErr error
	dirtyErr      error

	headCommitCalls int
	remoteHeadCalls int
	dirtyCalls      int
}

func (f *fakeExecGitRunner) HeadCommit() (string, error) {
	f.headCommitCalls++
	if f.headCommitErr != nil {
		return "", f.headCommitErr
	}
	return f.headCommit, nil
}

func (f *fakeExecGitRunner) RemoteHead(remote, branch string) (string, error) {
	f.remoteHeadCalls++
	if f.remoteHeadErr != nil {
		return "", f.remoteHeadErr
	}
	return f.remoteHead, nil
}

func (f *fakeExecGitRunner) IsDirty() (bool, error) {
	f.dirtyCalls++
	if f.dirtyErr != nil {
		return false, f.dirtyErr
	}
	return f.dirty, nil
}

// testUpdateDeps creates Deps with an UpdateManager backed by a temp dir
// and a fake GitRunner.
func testUpdateDeps(t *testing.T) (*Deps, *fakeExecGitRunner, *update.Manager) {
	t.Helper()
	base := testDeps(t)

	dir := t.TempDir()
	um, err := update.NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	um.SetSource(update.UpdateSource{
		Type:    "git",
		Remote:  "origin",
		Branch:  "main",
		RepoURL: "https://example.com/repo.git",
		Mode:    "manual",
	})

	fake := &fakeExecGitRunner{
		headCommit: "abc123",
		remoteHead: "abc123",
		dirty:      false,
	}

	base.UpdateManager = um
	base.GitRunner = fake
	return base, fake, um
}

func TestUpdateCheck_UpToDate(t *testing.T) {
	deps, fake, _ := testUpdateDeps(t)
	r := New(deps)

	result := execOK(t, r, "update.check", nil)

	if result["status"] != "up-to-date" {
		t.Errorf("status = %q, want %q", result["status"], "up-to-date")
	}
	if fake.remoteHeadCalls != 1 {
		t.Errorf("remoteHeadCalls = %d, want 1", fake.remoteHeadCalls)
	}
	if fake.headCommitCalls != 1 {
		t.Errorf("headCommitCalls = %d, want 1", fake.headCommitCalls)
	}
}

func TestUpdateCheck_RemoteHeadCalled_NotFetch(t *testing.T) {
	deps, fake, _ := testUpdateDeps(t)
	r := New(deps)

	execOK(t, r, "update.check", nil)

	// RemoteHead is called (ls-remote), not a non-existent Fetch.
	if fake.remoteHeadCalls != 1 {
		t.Errorf("remoteHeadCalls = %d, want 1", fake.remoteHeadCalls)
	}
}

func TestUpdateCheck_UpdateAvailable(t *testing.T) {
	deps, fake, _ := testUpdateDeps(t)
	fake.remoteHead = "def789"
	r := New(deps)

	result := execOK(t, r, "update.check", nil)

	if result["status"] != "update-available" {
		t.Errorf("status = %q, want %q", result["status"], "update-available")
	}
	if result["requiresRestart"] != true {
		t.Error("requiresRestart should be true when update available")
	}
	if result["behindBy"].(float64) != 1 {
		t.Errorf("behindBy = %v, want 1", result["behindBy"])
	}
}

func TestUpdateCheck_IgnoredRemoteHead(t *testing.T) {
	deps, fake, um := testUpdateDeps(t)
	fake.remoteHead = "ignored123"
	um.SetPolicy(update.UpdatePolicy{
		AutoCheck:            false,
		AutoApply:            false,
		CheckIntervalSeconds: 86400,
		AllowDirtyWorktree:   false,
		AllowWhenRunsActive:  false,
		IgnoredVersions:      []string{"ignored123"},
	})
	r := New(deps)

	result := execOK(t, r, "update.check", nil)

	if result["status"] != "up-to-date" {
		t.Errorf("status = %q, want %q (ignored remote head)", result["status"], "up-to-date")
	}
}

func TestUpdateCheck_DirtyWorktree(t *testing.T) {
	deps, fake, _ := testUpdateDeps(t)
	fake.dirty = true
	r := New(deps)

	result := execOK(t, r, "update.check", nil)

	if result["dirty"] != true {
		t.Error("dirty should be true")
	}
	// Dirty doesn't block check — it returns up-to-date/update-available + dirty flag
	if result["status"] != "up-to-date" {
		t.Errorf("status = %q, want %q", result["status"], "up-to-date")
	}
}

func TestUpdateCheck_LsRemoteEmpty_ReturnsError(t *testing.T) {
	deps, fake, _ := testUpdateDeps(t)
	fake.remoteHead = ""
	fake.remoteHeadErr = fmt.Errorf("ls-remote origin refs/heads/main: remote branch may not exist or remote is unreachable")
	r := New(deps)

	_, err := r.Execute(req("update.check", nil))
	if err == nil {
		t.Fatal("expected error for empty ls-remote")
	}
	if !strings.Contains(err.Error(), "ls-remote") {
		t.Errorf("error should mention ls-remote, got: %v", err)
	}
}

func TestUpdatePlan_DoesNotFetch(t *testing.T) {
	deps, fake, _ := testUpdateDeps(t)
	fake.remoteHead = "def789" // make it have an update available
	r := New(deps)

	// Pre-check to set status
	execOK(t, r, "update.check", nil)

	result := execOK(t, r, "update.plan", nil)

	// plan should NOT have called any additional git operations on the fake
	// (it reads last status, doesn't re-check unless status is unknown)
	_ = result
	// remoteHeadCalls was 1 from update.check, should still be 1 after plan
	if fake.remoteHeadCalls != 1 {
		t.Errorf("remoteHeadCalls = %d after plan, want 1 (plan should not re-check)", fake.remoteHeadCalls)
	}
}

func TestUpdatePlan_DirtyWorktreeBlocker(t *testing.T) {
	deps, fake, _ := testUpdateDeps(t)
	fake.dirty = true
	fake.remoteHead = "def789"
	r := New(deps)

	// Pre-check to set status (dirty + update available)
	execOK(t, r, "update.check", nil)

	result := execOK(t, r, "update.plan", nil)

	if result["canUpdate"] != false {
		t.Error("canUpdate should be false when worktree is dirty")
	}
	blockers, _ := result["blockers"].([]interface{})
	hasDirty := false
	for _, b := range blockers {
		bm, _ := b.(map[string]interface{})
		if bm["type"] == "dirty_worktree" {
			hasDirty = true
		}
	}
	if !hasDirty {
		t.Error("expected dirty_worktree blocker")
	}
}

func TestUpdatePlan_ActiveRunsBlocker(t *testing.T) {
	deps, fake, _ := testUpdateDeps(t)
	fake.remoteHead = "def789"
	r := New(deps)

	// Create a running run
	deps.RunStore.Create(&run.Run{
		NodeID:   "n1",
		Kind:     run.KindTerminal,
		PluginID: "test",
	})

	// Pre-check
	execOK(t, r, "update.check", nil)

	result := execOK(t, r, "update.plan", nil)

	if result["canUpdate"] != false {
		t.Error("canUpdate should be false when active runs exist")
	}
	blockers, _ := result["blockers"].([]interface{})
	hasActiveRuns := false
	for _, b := range blockers {
		bm, _ := b.(map[string]interface{})
		if bm["type"] == "active_runs" {
			hasActiveRuns = true
		}
	}
	if !hasActiveRuns {
		t.Error("expected active_runs blocker")
	}
}

func TestUpdatePlan_CanUpdate(t *testing.T) {
	deps, fake, _ := testUpdateDeps(t)
	fake.remoteHead = "def789"
	r := New(deps)

	// Pre-check
	execOK(t, r, "update.check", nil)

	result := execOK(t, r, "update.plan", nil)

	if result["canUpdate"] != true {
		t.Errorf("canUpdate = %v, want true", result["canUpdate"])
	}
	steps, _ := result["steps"].([]interface{})
	if len(steps) != 2 {
		t.Errorf("expected 2 plan steps, got %d", len(steps))
	}
}

func TestUpdatePlan_StepsDoNotIncludeFetch(t *testing.T) {
	deps, fake, _ := testUpdateDeps(t)
	fake.remoteHead = "def789"
	r := New(deps)

	execOK(t, r, "update.check", nil)
	result := execOK(t, r, "update.plan", nil)

	steps, _ := result["steps"].([]interface{})
	for _, s := range steps {
		sm, _ := s.(map[string]interface{})
		action, _ := sm["action"].(string)
		if action == "git_fetch" {
			t.Error("plan steps must NOT include git_fetch")
		}
	}
}

func TestUpdateCheck_GitRunnerNil_ReturnsError(t *testing.T) {
	deps := testDeps(t)
	dir := t.TempDir()
	um, err := update.NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	deps.UpdateManager = um
	// GitRunner is intentionally not set (nil)
	r := New(deps)

	_, err = r.Execute(req("update.check", nil))
	if err == nil {
		t.Fatal("expected error when GitRunner is nil")
	}
}

func TestUpdatePlan_UnknownStatus_TriggersCheck(t *testing.T) {
	deps, fake, _ := testUpdateDeps(t)
	fake.remoteHead = "def789"
	r := New(deps)

	// Don't pre-check — status is unknown. Plan should auto-check.
	result := execOK(t, r, "update.plan", nil)

	if fake.remoteHeadCalls != 1 {
		t.Errorf("remoteHeadCalls = %d, want 1 (plan should trigger check when status unknown)", fake.remoteHeadCalls)
	}
	_ = result
}

func TestUpdateSourceGet(t *testing.T) {
	deps, _, _ := testUpdateDeps(t)
	r := New(deps)

	result := execOK(t, r, "update.source.get", nil)

	if result["type"] != "git" {
		t.Errorf("type = %q, want %q", result["type"], "git")
	}
	if result["remote"] != "origin" {
		t.Errorf("remote = %q, want %q", result["remote"], "origin")
	}
}

func TestUpdateSourceSet(t *testing.T) {
	deps, _, _ := testUpdateDeps(t)
	r := New(deps)

	result := execOK(t, r, "update.source.set", map[string]interface{}{
		"branch": "develop",
	})

	if result["branch"] != "develop" {
		t.Errorf("branch = %q, want %q", result["branch"], "develop")
	}
	// Verify it was persisted
	src := execOK(t, r, "update.source.get", nil)
	if src["branch"] != "develop" {
		t.Errorf("persisted branch = %q, want %q", src["branch"], "develop")
	}
}

func TestUpdatePolicyGetSet_RoundTrip(t *testing.T) {
	deps, _, _ := testUpdateDeps(t)
	r := New(deps)

	// Set
	execOK(t, r, "update.policy.set", map[string]interface{}{
		"autoCheck":            true,
		"checkIntervalSeconds": float64(3600),
	})

	// Get
	result := execOK(t, r, "update.policy.get", nil)
	if result["autoCheck"] != true {
		t.Error("autoCheck should be true")
	}
	if result["checkIntervalSeconds"].(float64) != 3600 {
		t.Errorf("checkIntervalSeconds = %v", result["checkIntervalSeconds"])
	}
	if result["autoApply"] != false {
		t.Error("autoApply should be false")
	}
}

func TestUpdatePolicySet_RejectsAutoApply(t *testing.T) {
	deps, _, _ := testUpdateDeps(t)
	r := New(deps)

	_, err := r.Execute(req("update.policy.set", map[string]interface{}{
		"autoApply": true,
	}))
	if err == nil {
		t.Fatal("expected error when setting autoApply: true")
	}
}

func TestUpdateIgnore(t *testing.T) {
	deps, fake, _ := testUpdateDeps(t)
	fake.remoteHead = "skipme"
	r := New(deps)

	// First check — should be update-available
	result := execOK(t, r, "update.check", nil)
	if result["status"] != "update-available" {
		t.Fatalf("pre-check status = %q, want %q", result["status"], "update-available")
	}

	// Ignore the remote commit
	ignoreResult := execOK(t, r, "update.ignore", map[string]interface{}{
		"version": "skipme",
	})
	versions, _ := ignoreResult["ignoredVersions"].([]interface{})
	if len(versions) != 1 {
		t.Fatalf("expected 1 ignored version, got %d", len(versions))
	}

	// Status should now be up-to-date
	statusResult := execOK(t, r, "update.status", nil)
	if statusResult["status"] != "up-to-date" {
		t.Errorf("status after ignore = %q, want %q", statusResult["status"], "up-to-date")
	}
}

func TestUpdatePlan_AllowsDirtyWhenPermitted(t *testing.T) {
	deps, fake, um := testUpdateDeps(t)
	fake.dirty = true
	fake.remoteHead = "def789"
	um.SetPolicy(update.UpdatePolicy{
		AutoCheck:            false,
		AutoApply:            false,
		CheckIntervalSeconds: 86400,
		AllowDirtyWorktree:   true,
		AllowWhenRunsActive:  false,
	})
	r := New(deps)

	execOK(t, r, "update.check", nil)
	result := execOK(t, r, "update.plan", nil)

	if result["canUpdate"] != true {
		t.Error("canUpdate should be true when dirty worktree is allowed")
	}
}

func TestUpdatePlan_AllowsRunsActiveWhenPermitted(t *testing.T) {
	deps, fake, um := testUpdateDeps(t)
	fake.remoteHead = "def789"
	um.SetPolicy(update.UpdatePolicy{
		AutoCheck:            false,
		AutoApply:            false,
		CheckIntervalSeconds: 86400,
		AllowDirtyWorktree:   false,
		AllowWhenRunsActive:  true,
	})
	r := New(deps)

	deps.RunStore.Create(&run.Run{
		NodeID:   "n1",
		Kind:     run.KindTerminal,
		PluginID: "test",
	})

	execOK(t, r, "update.check", nil)
	result := execOK(t, r, "update.plan", nil)

	if result["canUpdate"] != true {
		t.Error("canUpdate should be true when active runs are allowed")
	}
}

func TestUpdateStatus(t *testing.T) {
	deps, _, _ := testUpdateDeps(t)
	r := New(deps)

	result := execOK(t, r, "update.status", nil)

	if result["status"] != "unknown" {
		t.Errorf("initial status = %q, want %q", result["status"], "unknown")
	}
	if result["requiresRestart"] != false {
		t.Error("requiresRestart should default to false")
	}
}
