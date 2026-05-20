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
	"github.com/user/sessionnode/go-core/internal/platform"
	"github.com/user/sessionnode/go-core/internal/pluginmanifest"
	"github.com/user/sessionnode/go-core/internal/process"
	"github.com/user/sessionnode/go-core/internal/session"
	"github.com/user/sessionnode/go-core/internal/testutil"
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
			ID:   "test-plugin",
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
				Permissions:  perms,
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

func TestPluginInstallPlan_ReturnsNotImplemented(t *testing.T) {
	r := New(testDeps(t))
	m := execOK(t, r, "plugin.install", map[string]string{"pluginId": "sessionnode-core"})
	if m["status"] != "not_implemented" {
		t.Errorf("status = %v, want not_implemented", m["status"])
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

// fullPluginDeps returns deps with Config, Notifier, and History initialized.
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

// TestNotImplementedStub verifies that stub capabilities return not_implemented.
func TestNotImplementedStub(t *testing.T) {
	r := New(testDeps(t))

	// Test each stub via direct registry call
	stubs := []string{
		"plugin.install.execute",
		"plugin.uninstall",
		"plugin.files.register",
	}
	for _, capName := range stubs {
		m := execOK(t, r, capName, map[string]string{"pluginId": "test"})
		if m["status"] != "not_implemented" {
			t.Errorf("%s: status = %v, want not_implemented", capName, m["status"])
		}
	}
}

// TestPluginPermissionsList_WithGrant verifies permissions list includes grant state.
func TestPluginPermissionsList_WithGrant(t *testing.T) {
	deps := fullPluginDeps(t)
	deps.Manifests = &mockManifestLoader{
		manifest: &pluginmanifest.Manifest{
			ID:   "test-plugin",
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
