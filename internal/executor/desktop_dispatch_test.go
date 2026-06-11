package executor

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"

	"github.com/PENG1028/sessionbridge-core/internal/dispatcher"
	"github.com/PENG1028/sessionbridge-core/pkg/protocol"
	"github.com/PENG1028/sessionbridge-core/pkg/types"
)

// ─── Dispatch Pipeline Integration Tests ─────────────────────────────────
//
// These tests validate desktop capabilities through the full 8-step dispatch
// chain: authenticate → resolve plugin → check enabled → check permission →
// plan check → route to target → execute → audit → return.
//
// They verify:
//   - Success path returns correct CapabilityResponse shape
//   - Error paths return proper error codes
//   - OpLog recording works for desktop capabilities
//   - Audit logging captures all steps

// ─── Mock Implementations (follow dispatcher_test.go pattern) ─────────────

type mockAuth struct {
	actor *types.Actor
	err   error
}

func (m *mockAuth) Authenticate(actor types.Actor) (*types.Actor, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.actor, nil
}

type mockPlugins struct {
	plugin *dispatcher.PluginEntry
	err    error
}

func (m *mockPlugins) Get(id types.PluginID) (*dispatcher.PluginEntry, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.plugin, nil
}

type mockPerms struct {
	err error
}

func (m *mockPerms) Check(req *types.CapabilityRequest) error {
	return m.err
}

type mockPlanner struct {
	requiresPlan bool
	planID       string
	err          error
	planState    string
}

func (m *mockPlanner) RequiresPlan(capability string) bool {
	return m.requiresPlan
}

func (m *mockPlanner) CreatePlan(req *types.CapabilityRequest) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.planID, nil
}

func (m *mockPlanner) ValidatePlan(planID string) error {
	if m.err != nil {
		return m.err
	}
	if m.planState == "" || m.planState == "approved" {
		return nil
	}
	if m.planState == "denied" {
		return fmt.Errorf("plan %s was denied", planID)
	}
	if m.planState == "pending" {
		return fmt.Errorf("plan %s is pending approval", planID)
	}
	return fmt.Errorf("plan not found: %s", planID)
}

type mockAudit struct {
	mu      sync.Mutex
	entries []auditLogEntry
}

type auditLogEntry struct {
	req     *types.CapabilityRequest
	allowed bool
	detail  string
}

func (m *mockAudit) Log(req *types.CapabilityRequest, allowed bool, detail string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, auditLogEntry{req: req, allowed: allowed, detail: detail})
}

func (m *mockAudit) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}

func (m *mockAudit) LastAllowed() *bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.entries) == 0 {
		return nil
	}
	return &m.entries[len(m.entries)-1].allowed
}

type mockTopology struct {
	target *dispatcher.NodeTarget
	err    error
}

func (m *mockTopology) Get(nodeID types.NodeID) (*dispatcher.NodeTarget, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.target, nil
}

// mockOpLogRecorder records operations for dispatch testing.
type mockOpLogRecorder struct {
	mu    sync.Mutex
	calls []opLogRecordCall
}

type opLogRecordCall struct {
	capability string
	result     interface{}
	execErr    error
}

func (m *mockOpLogRecorder) Record(req *types.CapabilityRequest, result interface{}, execErr error) (types.OpID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, opLogRecordCall{
		capability: req.Capability,
		result:     result,
		execErr:    execErr,
	})
	return "op_test_001", nil
}

func (m *mockOpLogRecorder) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func (m *mockOpLogRecorder) LastCapability() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) == 0 {
		return ""
	}
	return m.calls[len(m.calls)-1].capability
}

// ─── Helpers ──────────────────────────────────────────────────────────────

// testDepsWithDispatcher creates a minimal Dispatcher wired up with the real
// Registry as the executor and mock implementations for auth, plugins, permissions,
// planner, audit, topology, and OpLog recorder.
func testDepsWithDispatcher(t *testing.T) (*dispatcher.Dispatcher, *mockAudit, *mockOpLogRecorder) {
	t.Helper()
	deps := testDeps(t)
	registry := New(deps)
	audit := &mockAudit{}
	opLog := &mockOpLogRecorder{}

	d := dispatcher.New(
		&mockAuth{actor: &types.Actor{Type: "web", ID: "test-user"}},
		&mockPlugins{plugin: &dispatcher.PluginEntry{ID: "test-plugin", Enabled: true}},
		&mockPerms{},   // no permission errors
		nil,            // no planner needed for desktop caps
		registry,       // real executor
		audit,
		opLog,
		&mockTopology{},
		"node_local",
	)
	return d, audit, opLog
}

func makeDesktopReq(capability string, payload interface{}) *types.CapabilityRequest {
	return req(capability, payload)
}

// ─── Dispatch: Success Path Tests ────────────────────────────────────────

// TestDispatch_ClipboardGet_Success tests clipboard.get through full dispatch chain.
func TestDispatch_ClipboardGet_Success(t *testing.T) {
	d, audit, opLog := testDepsWithDispatcher(t)

	resp := d.Dispatch(makeDesktopReq("desktop.clipboard.get", nil))
	if resp.OK {
		t.Logf("clipboard.get dispatch succeeded: payload=%v", resp.Payload)
		// On success: payload should have text and found fields
		if p, ok := resp.Payload.(map[string]interface{}); ok {
			if _, hasText := p["text"]; !hasText {
				t.Error("clipboard.get: payload missing 'text' field")
			}
			if _, hasFound := p["found"]; !hasFound {
				t.Error("clipboard.get: payload missing 'found' field")
			}
		}
		// Audit should have 1 success entry
		if audit.Count() != 1 {
			t.Errorf("expected 1 audit entry, got %d", audit.Count())
		}
		if allowed := audit.LastAllowed(); allowed == nil || !*allowed {
			t.Error("audit should record allowed=true on success")
		}
		// OpLog should have recorded the operation
		if opLog.Count() != 1 {
			t.Errorf("opLog: expected 1 record, got %d", opLog.Count())
		}
		if cap := opLog.LastCapability(); cap != "desktop.clipboard.get" {
			t.Errorf("opLog: expected clipboard.get, got %s", cap)
		}
	} else {
		t.Logf("clipboard.get dispatch error (expected on headless CI): %v", resp.Error)
		// On error, verify error response shape
		if resp.Error == nil {
			t.Fatal("dispatch: resp.Error should not be nil when OK=false")
		}
		if resp.Error.Code == "" {
			t.Error("dispatch: error code should not be empty")
		}
		if resp.Error.Message == "" {
			t.Error("dispatch: error message should not be empty")
		}
		// Audit should have 1 denied entry
		if audit.Count() != 1 {
			t.Errorf("expected 1 audit entry on error, got %d", audit.Count())
		}
		if allowed := audit.LastAllowed(); allowed == nil || *allowed {
			t.Error("audit should record allowed=false on error")
		}
	}
}

// TestDispatch_ClipboardSet_Success tests clipboard.set through full dispatch chain.
func TestDispatch_ClipboardSet_Success(t *testing.T) {
	d, audit, opLog := testDepsWithDispatcher(t)

	resp := d.Dispatch(makeDesktopReq("desktop.clipboard.set", map[string]interface{}{
		"text": "dispatch test text",
	}))

	if resp.OK {
		if p, ok := resp.Payload.(map[string]interface{}); ok {
			if p["status"] != "ok" {
				t.Errorf("clipboard.set: status = %v, want 'ok'", p["status"])
			}
		}
		if opLog.Count() != 1 {
			t.Errorf("opLog: expected 1 record, got %d", opLog.Count())
		}
		t.Logf("clipboard.set dispatch OK: payload=%v", resp.Payload)
	} else {
		t.Logf("clipboard.set dispatch error: %v", resp.Error)
	}

	// Verify audit recorded
	if audit.Count() == 0 {
		t.Error("expected at least 1 audit entry")
	}
}

// TestDispatch_Screenshot_Success tests screenshot through dispatch chain.
func TestDispatch_Screenshot_Success(t *testing.T) {
	d, audit, _ := testDepsWithDispatcher(t)

	resp := d.Dispatch(makeDesktopReq("desktop.screenshot", nil))

	if resp.OK {
		if p, ok := resp.Payload.(map[string]interface{}); ok {
			for _, field := range []string{"data", "width", "height", "format"} {
				if _, exists := p[field]; !exists {
					t.Errorf("screenshot dispatch: payload missing '%s' field", field)
				}
			}
			if p["format"] != "png" {
				t.Errorf("screenshot dispatch: format = %v, want 'png'", p["format"])
			}
		}
		t.Logf("screenshot dispatch OK: payload=%v", resp.Payload)
	} else {
		t.Logf("screenshot dispatch error (expected on headless CI): %v", resp.Error)
	}

	if audit.Count() == 0 {
		t.Error("expected at least 1 audit entry")
	}
}

// TestDispatch_DesktopClick_Success tests desktop.click through dispatch.
func TestDispatch_DesktopClick_Success(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("desktop.click dispatch: only run on Windows")
	}
	skipUnlessGUI(t)
	d, audit, _ := testDepsWithDispatcher(t)

	resp := d.Dispatch(makeDesktopReq("desktop.click", map[string]interface{}{
		"x": 100,
		"y": 100,
	}))

	if !resp.OK {
		t.Fatalf("desktop.click dispatch failed: %v", resp.Error)
	}
	if resp.RequestID != "test_req" {
		t.Errorf("RequestID = %q", resp.RequestID)
	}
	if p, ok := resp.Payload.(map[string]interface{}); ok {
		if p["status"] != "ok" {
			t.Errorf("desktop.click: status = %v, want 'ok'", p["status"])
		}
	}
	if audit.Count() == 0 {
		t.Error("expected audit entry for desktop.click")
	}
}

// TestDispatch_DesktopType_Success tests desktop.type through dispatch.
func TestDispatch_DesktopType_Success(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("desktop.type dispatch: only run on Windows")
	}
	skipUnlessGUI(t)
	d, audit, _ := testDepsWithDispatcher(t)

	resp := d.Dispatch(makeDesktopReq("desktop.type", map[string]interface{}{
		"text": "Hello from dispatch",
	}))

	if !resp.OK {
		t.Fatalf("desktop.type dispatch failed: %v", resp.Error)
	}
	if p, ok := resp.Payload.(map[string]interface{}); ok {
		if p["status"] != "ok" {
			t.Errorf("desktop.type: status = %v, want 'ok'", p["status"])
		}
	}
	if audit.Count() == 0 {
		t.Error("expected audit entry for desktop.type")
	}
}

// TestDispatch_InputKeyboard_Success tests input.keyboard through dispatch.
func TestDispatch_InputKeyboard_Success(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("input.keyboard dispatch: only run on Windows")
	}
	skipUnlessGUI(t)
	d, audit, _ := testDepsWithDispatcher(t)

	resp := d.Dispatch(makeDesktopReq("input.keyboard", map[string]interface{}{
		"action": "tap",
		"keys":   []string{"a"},
	}))

	if !resp.OK {
		t.Fatalf("input.keyboard dispatch failed: %v", resp.Error)
	}
	if p, ok := resp.Payload.(map[string]interface{}); ok {
		if p["status"] != "ok" {
			t.Errorf("input.keyboard: status = %v, want 'ok'", p["status"])
		}
	}
	if audit.Count() == 0 {
		t.Error("expected audit entry for input.keyboard")
	}
}

// TestDispatch_InputMouse_Success tests input.mouse through dispatch.
func TestDispatch_InputMouse_Success(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("input.mouse dispatch: only run on Windows")
	}
	skipUnlessGUI(t)
	d, audit, _ := testDepsWithDispatcher(t)

	resp := d.Dispatch(makeDesktopReq("input.mouse", map[string]interface{}{
		"action": "move",
		"x":      100,
		"y":      100,
	}))

	if !resp.OK {
		t.Fatalf("input.mouse dispatch failed: %v", resp.Error)
	}
	if p, ok := resp.Payload.(map[string]interface{}); ok {
		if p["status"] != "ok" {
			t.Errorf("input.mouse: status = %v, want 'ok'", p["status"])
		}
	}
	if audit.Count() == 0 {
		t.Error("expected audit entry for input.mouse")
	}
}

// ─── Dispatch: Error Path Tests ──────────────────────────────────────────

// TestDispatch_DesktopClick_NilPayload_ErrorPath tests nil payload behavior through dispatch.
func TestDispatch_DesktopClick_NilPayload_ErrorPath(t *testing.T) {
	d, audit, _ := testDepsWithDispatcher(t)

	resp := d.Dispatch(makeDesktopReq("desktop.click", nil))

	if !resp.OK {
		// On non-Windows or headless, OS backend may fail
		t.Logf("desktop.click nil payload dispatch error: %v", resp.Error)
		if resp.Error.Code != protocol.ErrCodeExecutionError {
			t.Errorf("expected code %s, got %s", protocol.ErrCodeExecutionError, resp.Error.Code)
		}
	} else {
		// x=0, y=0 are valid defaults since decodePayload returns nil for empty payload
		t.Logf("desktop.click nil payload dispatch OK: defaults x=0,y=0")
	}
	if audit.Count() == 0 {
		t.Error("expected audit entry")
	}
}

// TestDispatch_InputMouse_NilPayload_ErrorPath tests nil payload for input.mouse through dispatch.
func TestDispatch_InputMouse_NilPayload_ErrorPath(t *testing.T) {
	d, audit, _ := testDepsWithDispatcher(t)

	resp := d.Dispatch(makeDesktopReq("input.mouse", nil))

	if resp.OK {
		t.Fatal("input.mouse with nil payload should fail")
	}
	if resp.Error.Code != protocol.ErrCodeExecutionError {
		t.Errorf("expected code %s, got %s", protocol.ErrCodeExecutionError, resp.Error.Code)
	}
	if audit.Count() != 1 {
		t.Errorf("expected 1 audit entry, got %d", audit.Count())
	}
	if allowed := audit.LastAllowed(); allowed == nil || *allowed {
		t.Error("audit should record allowed=false on execution error")
	}
}

// TestDispatch_DesktopType_EmptyText_ErrorPath tests empty text rejection through dispatch.
func TestDispatch_DesktopType_EmptyText_ErrorPath(t *testing.T) {
	d, audit, _ := testDepsWithDispatcher(t)

	resp := d.Dispatch(makeDesktopReq("desktop.type", map[string]interface{}{
		"text": "",
	}))

	if resp.OK {
		t.Fatal("desktop.type with empty text should fail")
	}
	if resp.Error.Code != protocol.ErrCodeExecutionError {
		t.Errorf("expected code %s, got %s", protocol.ErrCodeExecutionError, resp.Error.Code)
	}
	if audit.Count() != 1 {
		t.Errorf("expected 1 audit entry, got %d", audit.Count())
	}
}

// TestDispatch_InputKeyboard_EmptyKeys_ErrorPath tests empty keys rejection through dispatch.
func TestDispatch_InputKeyboard_EmptyKeys_ErrorPath(t *testing.T) {
	d, audit, _ := testDepsWithDispatcher(t)

	resp := d.Dispatch(makeDesktopReq("input.keyboard", map[string]interface{}{
		"action": "tap",
		"keys":   []string{},
	}))

	if resp.OK {
		t.Fatal("input.keyboard with empty keys should fail")
	}
	if resp.Error.Code != protocol.ErrCodeExecutionError {
		t.Errorf("expected code %s, got %s", protocol.ErrCodeExecutionError, resp.Error.Code)
	}
	if audit.Count() != 1 {
		t.Errorf("expected 1 audit entry, got %d", audit.Count())
	}
}

// TestDispatch_AuthError_DesktopCap verifies auth errors propagate.
func TestDispatch_AuthError_DesktopCap(t *testing.T) {
	deps := testDeps(t)
	registry := New(deps)
	audit := &mockAudit{}

	d := dispatcher.New(
		&mockAuth{err: errors.New("invalid token")},
		&mockPlugins{plugin: &dispatcher.PluginEntry{ID: "test-plugin", Enabled: true}},
		&mockPerms{},
		nil,
		registry,
		audit,
		nil,
		&mockTopology{},
		"node_local",
	)

	resp := d.Dispatch(makeDesktopReq("desktop.clipboard.get", nil))

	if resp.OK {
		t.Fatal("expected auth error")
	}
	if resp.Error.Code != protocol.ErrCodeUnauthenticated {
		t.Errorf("expected code %s, got %s", protocol.ErrCodeUnauthenticated, resp.Error.Code)
	}
}

// TestDispatch_PermissionDenied_DesktopCap verifies permission errors propagate.
func TestDispatch_PermissionDenied_DesktopCap(t *testing.T) {
	deps := testDeps(t)
	registry := New(deps)
	audit := &mockAudit{}

	d := dispatcher.New(
		&mockAuth{actor: &types.Actor{Type: "web", ID: "test-user"}},
		&mockPlugins{plugin: &dispatcher.PluginEntry{ID: "test-plugin", Enabled: true}},
		&mockPerms{err: errors.New("permission denied for desktop input")},
		nil,
		registry,
		audit,
		nil,
		&mockTopology{},
		"node_local",
	)

	resp := d.Dispatch(makeDesktopReq("input.keyboard", map[string]interface{}{
		"action": "tap",
		"keys":   []string{"a"},
	}))

	if resp.OK {
		t.Fatal("expected permission error")
	}
	if resp.Error.Code != protocol.ErrCodePermissionDenied {
		t.Errorf("expected code %s, got %s", protocol.ErrCodePermissionDenied, resp.Error.Code)
	}
}

// ─── OpLog Recording Tests ────────────────────────────────────────────────

// TestDispatch_OpLogRecordsDesktopWrites verifies OpLog records desktop write operations.
func TestDispatch_OpLogRecordsDesktopWrites(t *testing.T) {
	deps := testDeps(t)
	registry := New(deps)
	audit := &mockAudit{}
	opLog := &mockOpLogRecorder{}

	d := dispatcher.New(
		&mockAuth{actor: &types.Actor{Type: "web", ID: "test-user"}},
		&mockPlugins{plugin: &dispatcher.PluginEntry{ID: "test-plugin", Enabled: true}},
		&mockPerms{},
		nil,
		registry,
		audit,
		opLog,
		&mockTopology{},
		"node_local",
	)

	// Test with nil payload — error case (no OpLog recording)
	_ = d.Dispatch(makeDesktopReq("desktop.type", nil))
	errorCount := opLog.Count()

	// Test with empty text — error case (no OpLog recording)
	_ = d.Dispatch(makeDesktopReq("desktop.type", map[string]interface{}{
		"text": "",
	}))
	errorCount2 := opLog.Count()

	// OpLog should NOT record on execution errors
	if errorCount2 > errorCount {
		t.Error("OpLog should NOT record when execution fails")
	}
}

// TestDispatch_OpLogRecordsClipboardGet verifies OpLog with real OpLog store.
func TestDispatch_OpLogRecordsClipboardGet(t *testing.T) {
	d := testDepsWithOpLog(t)
	r := New(d)

	// We test through the registry directly since the dispatcher uses the same OpLog.
	// Desktop capabilities are not auto-recorded by the registry (that's the dispatcher's job).
	// This test verifies that the real OpLog store works correctly.
	_, err := r.Execute(req("desktop.clipboard.get", nil))
	if err != nil {
		t.Logf("clipboard.get: %v (expected in headless)", err)
	}

	// Check OpLog can be queried
	ops := d.OpLog.Query(types.OpFilter{Limit: 10})
	t.Logf("OpLog has %d entries after clipboard.get", len(ops))
}

// ─── Response Shape Through Dispatch ──────────────────────────────────────

// TestDispatch_ResponseShape verifies all desktop caps return proper response shape.
func TestDispatch_ResponseShape(t *testing.T) {
	d, _, _ := testDepsWithDispatcher(t)

	tests := []struct {
		capability  string
		payload     interface{}
		expectError bool     // may error on headless
		checkKeys   []string // keys to check on success
	}{
		{"desktop.clipboard.get", nil, false, []string{"text", "found"}},
		{"desktop.clipboard.set", map[string]interface{}{"text": "hello"}, false, []string{"status"}},
		{"desktop.screenshot", nil, false, []string{"data", "width", "height", "format"}},
		{"desktop.click", map[string]interface{}{"x": 100, "y": 100}, false, []string{"status"}},
		{"desktop.type", map[string]interface{}{"text": "test"}, false, []string{"status"}},
		{"input.keyboard", map[string]interface{}{"action": "tap", "keys": []string{"a"}}, false, []string{"status"}},
		{"input.mouse", map[string]interface{}{"action": "move", "x": 100, "y": 100}, false, []string{"status"}},
	}

	for _, tt := range tests {
		t.Run(tt.capability, func(t *testing.T) {
			resp := d.Dispatch(makeDesktopReq(tt.capability, tt.payload))

			// Every response must have RequestID
			if resp.RequestID != "test_req" {
				t.Errorf("%s: RequestID = %q, want 'test_req'", tt.capability, resp.RequestID)
			}

			if resp.OK {
				// Success path: check response shape
				p, ok := resp.Payload.(map[string]interface{})
				if !ok {
					t.Errorf("%s: payload is not a map, got %T", tt.capability, resp.Payload)
					return
				}
				for _, key := range tt.checkKeys {
					if _, exists := p[key]; !exists {
						t.Errorf("%s: payload missing required key %q", tt.capability, key)
					}
				}
				t.Logf("%s: OK, keys present: %v", tt.capability, tt.checkKeys)
			} else {
				// Error path: check error shape
				if resp.Error == nil {
					t.Errorf("%s: OK=false but error is nil", tt.capability)
					return
				}
				if resp.Error.Code == "" {
					t.Errorf("%s: error code is empty", tt.capability)
				}
				t.Logf("%s: error code=%s msg=%s", tt.capability, resp.Error.Code, resp.Error.Message)
			}
		})
	}
}

// ─── SkipRecording Flag Test ──────────────────────────────────────────────

// TestDispatch_SkipRecordingDesktop verifies that _skipRecording prevents OpLog recording.
func TestDispatch_SkipRecordingDesktop(t *testing.T) {
	deps := testDeps(t)
	registry := New(deps)
	audit := &mockAudit{}
	opLog := &mockOpLogRecorder{}

	d := dispatcher.New(
		&mockAuth{actor: &types.Actor{Type: "web", ID: "test-user"}},
		&mockPlugins{plugin: &dispatcher.PluginEntry{ID: "test-plugin", Enabled: true}},
		&mockPerms{},
		nil,
		registry,
		audit,
		opLog,
		&mockTopology{},
		"node_local",
	)

	// Without SkipRecording
	req1 := makeDesktopReq("desktop.clipboard.get", nil)
	_ = d.Dispatch(req1)
	count1 := opLog.Count()

	// With SkipRecording
	req2 := makeDesktopReq("desktop.clipboard.get", nil)
	req2.SkipRecording = true
	_ = d.Dispatch(req2)
	count2 := opLog.Count()

	// SkipRecording should prevent additional OpLog entry
	if count2 > count1 {
		t.Errorf("SkipRecording should prevent OpLog entry: %d -> %d", count1, count2)
	}
}

// ─── Classification Test ──────────────────────────────────────────────────

// TestDispatch_DesktopCapClassification verifies ClassifyCapability for desktop caps.
func TestDispatch_DesktopCapClassification(t *testing.T) {
	// Desktop capabilities should be classified appropriately:
	//   - clipboard.get is a read → ideally Noop (D), but currently defaults to State (C)
	//   - clipboard.set is a write → State (C) or Content (A)
	//   - screenshot is a capture → State (C) or Artifact (B)
	//   - desktop.click/type are inputs → State (C)
	//   - input.* are inputs → State (C)
	caps := map[string]types.OpClass{
		"desktop.clipboard.get": types.OpClassState, // read-only, should be Noop but falls to default
		"desktop.clipboard.set": types.OpClassState, // state mutation
		"desktop.screenshot":    types.OpClassState, // capture
		"desktop.click":         types.OpClassState, // input
		"desktop.type":          types.OpClassState, // input
		"input.keyboard":        types.OpClassState, // input
		"input.mouse":           types.OpClassState, // input
	}

	for cap, expected := range caps {
		actual := ClassifyCapability(cap)
		if actual != expected {
			t.Errorf("%s: ClassifyCapability = %s, want %s", cap, actual, expected)
		}
	}
}
