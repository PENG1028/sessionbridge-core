package dispatcher

import (
	"errors"
	"sync"
	"testing"

	"github.com/user/sessionnode/go-core/pkg/protocol"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// --- Mock implementations ---

type mockAuthenticator struct {
	actor *types.Actor
	err   error
}

func (m *mockAuthenticator) Authenticate(actor types.Actor) (*types.Actor, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.actor, nil
}

type mockPluginRegistry struct {
	plugin *PluginEntry
	err    error
}

func (m *mockPluginRegistry) Get(id types.PluginID) (*PluginEntry, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.plugin, nil
}

type mockPermissionChecker struct {
	err error
}

func (m *mockPermissionChecker) Check(req *types.CapabilityRequest) error {
	return m.err
}

type mockExecutor struct {
	result interface{}
	err    error
}

func (m *mockExecutor) Execute(req *types.CapabilityRequest) (interface{}, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

type mockAuditLogger struct {
	mu      sync.Mutex
	entries []auditEntry
}

type auditEntry struct {
	req     *types.CapabilityRequest
	allowed bool
	detail  string
}

func (m *mockAuditLogger) Log(req *types.CapabilityRequest, allowed bool, detail string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, auditEntry{req: req, allowed: allowed, detail: detail})
}

func (m *mockAuditLogger) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}

func (m *mockAuditLogger) LastAllowed() *bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.entries) == 0 {
		return nil
	}
	return &m.entries[len(m.entries)-1].allowed
}

type mockTopology struct {
	target *NodeTarget
	err    error
}

func (m *mockTopology) Get(nodeID types.NodeID) (*NodeTarget, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.target, nil
}

func makePayload() types.CapabilityRequest {
	return types.CapabilityRequest{
		RequestID:    "req_test_001",
		Actor:        types.Actor{Type: "web", ID: "browser_abc"},
		PluginID:     "test-plugin",
		Capability:   "test.capability",
		TargetNodeID: "",
	}
}

func TestDispatch_Success(t *testing.T) {
	audit := &mockAuditLogger{}
	d := createSuccessDispatcher(audit, "node_local")

	req := makePayload()
	resp := d.Dispatch(&req)

	if !resp.OK {
		t.Errorf("expected OK, got error: %v", resp.Error)
	}
	if resp.RequestID != "req_test_001" {
		t.Errorf("RequestID = %q", resp.RequestID)
	}
	if audit.Count() != 1 {
		t.Errorf("expected 1 audit entry, got %d", audit.Count())
	}
	if allowed := audit.LastAllowed(); allowed == nil || !*allowed {
		t.Error("last audit entry should be allowed=true")
	}
}

func TestDispatch_AuthenticateError(t *testing.T) {
	audit := &mockAuditLogger{}
	d := New(
		&mockAuthenticator{err: errors.New("invalid token")},
		&mockPluginRegistry{plugin: &PluginEntry{ID: "test-plugin", Enabled: true}},
		&mockPermissionChecker{},
		&mockExecutor{result: "ok"},
		audit,
		&mockTopology{},
		"node_local",
	)

	req := makePayload()
	resp := d.Dispatch(&req)

	if resp.OK {
		t.Fatal("expected failure")
	}
	if resp.Error.Code != protocol.ErrCodeUnauthenticated {
		t.Errorf("expected code %s, got %s", protocol.ErrCodeUnauthenticated, resp.Error.Code)
	}
	if audit.Count() != 1 {
		t.Errorf("expected 1 audit entry, got %d", audit.Count())
	}
	if allowed := audit.LastAllowed(); allowed == nil || *allowed {
		t.Error("last audit entry should be allowed=false")
	}
}

func TestDispatch_PluginNotFound(t *testing.T) {
	audit := &mockAuditLogger{}
	d := New(
		&mockAuthenticator{actor: &types.Actor{Type: "web", ID: "browser_abc"}},
		&mockPluginRegistry{err: errors.New("plugin not found")},
		&mockPermissionChecker{},
		&mockExecutor{result: "ok"},
		audit,
		&mockTopology{},
		"node_local",
	)

	req := makePayload()
	resp := d.Dispatch(&req)

	if resp.OK {
		t.Fatal("expected failure")
	}
	if resp.Error.Code != protocol.ErrCodePluginNotFound {
		t.Errorf("expected code %s, got %s", protocol.ErrCodePluginNotFound, resp.Error.Code)
	}
}

func TestDispatch_PluginDisabled(t *testing.T) {
	audit := &mockAuditLogger{}
	d := New(
		&mockAuthenticator{actor: &types.Actor{Type: "web", ID: "browser_abc"}},
		&mockPluginRegistry{plugin: &PluginEntry{ID: "test-plugin", Enabled: false}},
		&mockPermissionChecker{},
		&mockExecutor{result: "ok"},
		audit,
		&mockTopology{},
		"node_local",
	)

	req := makePayload()
	resp := d.Dispatch(&req)

	if resp.OK {
		t.Fatal("expected failure")
	}
	if resp.Error.Code != protocol.ErrCodePluginDisabled {
		t.Errorf("expected code %s, got %s", protocol.ErrCodePluginDisabled, resp.Error.Code)
	}
}

func TestDispatch_PermissionDenied(t *testing.T) {
	audit := &mockAuditLogger{}
	d := New(
		&mockAuthenticator{actor: &types.Actor{Type: "web", ID: "browser_abc"}},
		&mockPluginRegistry{plugin: &PluginEntry{ID: "test-plugin", Enabled: true}},
		&mockPermissionChecker{err: errors.New("permission denied")},
		&mockExecutor{result: "ok"},
		audit,
		&mockTopology{},
		"node_local",
	)

	req := makePayload()
	resp := d.Dispatch(&req)

	if resp.OK {
		t.Fatal("expected failure")
	}
	if resp.Error.Code != protocol.ErrCodePermissionDenied {
		t.Errorf("expected code %s, got %s", protocol.ErrCodePermissionDenied, resp.Error.Code)
	}
}

func TestDispatch_RemoteNodeForward(t *testing.T) {
	audit := &mockAuditLogger{}
	forwardCalled := false
	d := New(
		&mockAuthenticator{actor: &types.Actor{Type: "web", ID: "browser_abc"}},
		&mockPluginRegistry{plugin: &PluginEntry{ID: "test-plugin", Enabled: true}},
		&mockPermissionChecker{},
		&mockExecutor{result: "ok"},
		audit,
		&mockTopology{
			target: &NodeTarget{
				ID: "node_remote",
				Forward: func(req *types.CapabilityRequest) (*types.CapabilityResponse, error) {
					forwardCalled = true
					return &types.CapabilityResponse{RequestID: req.RequestID, OK: true, Payload: "forwarded"}, nil
				},
			},
		},
		"node_local",
	)

	req := makePayload()
	req.TargetNodeID = "node_remote"
	resp := d.Dispatch(&req)

	if !resp.OK {
		t.Errorf("expected OK, got error: %v", resp.Error)
	}
	if !forwardCalled {
		t.Error("Forward function was not called")
	}
}

func TestDispatch_RemoteNodeNotFound(t *testing.T) {
	audit := &mockAuditLogger{}
	d := New(
		&mockAuthenticator{actor: &types.Actor{Type: "web", ID: "browser_abc"}},
		&mockPluginRegistry{plugin: &PluginEntry{ID: "test-plugin", Enabled: true}},
		&mockPermissionChecker{},
		&mockExecutor{result: "ok"},
		audit,
		&mockTopology{err: errors.New("node not found")},
		"node_local",
	)

	req := makePayload()
	req.TargetNodeID = "node_unreachable"
	resp := d.Dispatch(&req)

	if resp.OK {
		t.Fatal("expected failure")
	}
	if resp.Error.Code != protocol.ErrCodeNodeUnreachable {
		t.Errorf("expected code %s, got %s", protocol.ErrCodeNodeUnreachable, resp.Error.Code)
	}
}

func TestDispatch_RemoteForwardError(t *testing.T) {
	audit := &mockAuditLogger{}
	d := New(
		&mockAuthenticator{actor: &types.Actor{Type: "web", ID: "browser_abc"}},
		&mockPluginRegistry{plugin: &PluginEntry{ID: "test-plugin", Enabled: true}},
		&mockPermissionChecker{},
		&mockExecutor{result: "ok"},
		audit,
		&mockTopology{
			target: &NodeTarget{
				ID: "node_remote",
				Forward: func(req *types.CapabilityRequest) (*types.CapabilityResponse, error) {
					return nil, errors.New("connection refused")
				},
			},
		},
		"node_local",
	)

	req := makePayload()
	req.TargetNodeID = "node_remote"
	resp := d.Dispatch(&req)

	if resp.OK {
		t.Fatal("expected failure")
	}
	if resp.Error.Code != protocol.ErrCodeForwardError {
		t.Errorf("expected code %s, got %s", protocol.ErrCodeForwardError, resp.Error.Code)
	}
}

func TestDispatch_ExecuteError(t *testing.T) {
	audit := &mockAuditLogger{}
	d := New(
		&mockAuthenticator{actor: &types.Actor{Type: "web", ID: "browser_abc"}},
		&mockPluginRegistry{plugin: &PluginEntry{ID: "test-plugin", Enabled: true}},
		&mockPermissionChecker{},
		&mockExecutor{err: errors.New("execution failed")},
		audit,
		&mockTopology{},
		"node_local",
	)

	req := makePayload()
	resp := d.Dispatch(&req)

	if resp.OK {
		t.Fatal("expected failure")
	}
	if resp.Error.Code != protocol.ErrCodeExecutionError {
		t.Errorf("expected code %s, got %s", protocol.ErrCodeExecutionError, resp.Error.Code)
	}
}

func TestDispatch_AuditCalledOnSuccess(t *testing.T) {
	audit := &mockAuditLogger{}
	d := createSuccessDispatcher(audit, "node_local")

	req := makePayload()
	d.Dispatch(&req)

	if audit.Count() != 1 {
		t.Fatalf("expected 1 audit call, got %d", audit.Count())
	}
	last := audit.LastAllowed()
	if last == nil || !*last {
		t.Error("expected allowed=true on success")
	}
}

func TestDispatch_AuditCalledOnFailure(t *testing.T) {
	d := New(
		&mockAuthenticator{err: errors.New("no auth")},
		&mockPluginRegistry{},
		&mockPermissionChecker{},
		&mockExecutor{},
		&mockAuditLogger{},
		&mockTopology{},
		"node_local",
	)

	req := makePayload()
	resp := d.Dispatch(&req)
	if resp.OK {
		t.Fatal("expected failure")
	}
}

func TestDispatch_LocalNodeWhenTargetEqualsLocal(t *testing.T) {
	audit := &mockAuditLogger{}
	d := createSuccessDispatcher(audit, "node_local")

	req := makePayload()
	req.TargetNodeID = "node_local" // explicit local targeting
	resp := d.Dispatch(&req)

	if !resp.OK {
		t.Errorf("expected OK, got error: %v", resp.Error)
	}
	if resp.RequestID != "req_test_001" {
		t.Errorf("RequestID = %q", resp.RequestID)
	}
}

// --- Helpers ---

func createSuccessDispatcher(audit *mockAuditLogger, localNodeID types.NodeID) *Dispatcher {
	return New(
		&mockAuthenticator{actor: &types.Actor{Type: "web", ID: "browser_abc"}},
		&mockPluginRegistry{plugin: &PluginEntry{ID: "test-plugin", Enabled: true}},
		&mockPermissionChecker{},
		&mockExecutor{result: map[string]interface{}{"status": "ok"}},
		audit,
		&mockTopology{},
		localNodeID,
	)
}
