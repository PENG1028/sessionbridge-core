package dispatcher

import (
	"errors"
	"fmt"
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

type mockPlanner struct {
	requiresPlan bool
	planID       string
	err          error
	planState    string // plan state for ValidatePlan
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

func (m *mockAuditLogger) LastDetail() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.entries) == 0 {
		return ""
	}
	return m.entries[len(m.entries)-1].detail
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

// --- Tests ---

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
		nil, /* no planner */
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
		nil, /* no planner */
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
		nil, /* no planner */
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
		nil, /* no planner */
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
		nil, /* no planner */
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
		nil, /* no planner */
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
		nil, /* no planner */
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
		nil, /* no planner */
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
		nil, /* no planner */
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
	req.TargetNodeID = "node_local"
	resp := d.Dispatch(&req)

	if !resp.OK {
		t.Errorf("expected OK, got error: %v", resp.Error)
	}
	if resp.RequestID != "req_test_001" {
		t.Errorf("RequestID = %q", resp.RequestID)
	}
}

// --- Plan Before Apply tests ---

func TestDispatch_PlanRequired_CreatesPlan(t *testing.T) {
	audit := &mockAuditLogger{}
	d := New(
		&mockAuthenticator{actor: &types.Actor{Type: "web", ID: "browser_abc"}},
		&mockPluginRegistry{plugin: &PluginEntry{ID: "test-plugin", Enabled: true}},
		&mockPermissionChecker{},
		&mockPlanner{requiresPlan: true, planID: "plan_001"},
		&mockExecutor{result: "ok"},
		audit,
		&mockTopology{},
		"node_local",
	)

	req := makePayload()
	req.Capability = "plugin.install"
	resp := d.Dispatch(&req)

	if resp.OK {
		t.Fatal("expected plan-pending response, not OK")
	}
	if resp.Error == nil || resp.Error.Code != protocol.ErrCodePlanRequired {
		t.Errorf("expected code %s, got %v", protocol.ErrCodePlanRequired, resp.Error)
	}
	if resp.PlanID != "plan_001" {
		t.Errorf("PlanID = %q, want %q", resp.PlanID, "plan_001")
	}
	if resp.PlanState != "pending" {
		t.Errorf("PlanState = %q, want %q", resp.PlanState, "pending")
	}
	// Audit should record plan creation
	if detail := audit.LastDetail(); detail != "plan created: plan_001" {
		t.Errorf("audit detail = %q, want %q", detail, "plan created: plan_001")
	}
}

func TestDispatch_PlanNotRequired_PassesThrough(t *testing.T) {
	audit := &mockAuditLogger{}
	d := New(
		&mockAuthenticator{actor: &types.Actor{Type: "web", ID: "browser_abc"}},
		&mockPluginRegistry{plugin: &PluginEntry{ID: "test-plugin", Enabled: true}},
		&mockPermissionChecker{},
		&mockPlanner{requiresPlan: false},
		&mockExecutor{result: "executed"},
		audit,
		&mockTopology{},
		"node_local",
	)

	req := makePayload()
	resp := d.Dispatch(&req)

	if !resp.OK {
		t.Fatalf("expected OK, got error: %v", resp.Error)
	}
	if resp.Payload != "executed" {
		t.Errorf("Payload = %v, want %q", resp.Payload, "executed")
	}
}

func TestDispatch_PlanWithPlanID_SkipsPlanCheck(t *testing.T) {
	audit := &mockAuditLogger{}
	d := New(
		&mockAuthenticator{actor: &types.Actor{Type: "web", ID: "browser_abc"}},
		&mockPluginRegistry{plugin: &PluginEntry{ID: "test-plugin", Enabled: true}},
		&mockPermissionChecker{},
		&mockPlanner{requiresPlan: true, planID: "plan_001"},
		&mockExecutor{result: "approved-execution"},
		audit,
		&mockTopology{},
		"node_local",
	)

	req := makePayload()
	req.Capability = "plugin.install"
	req.PlanID = "plan_001" // pre-approved plan
	resp := d.Dispatch(&req)

	if !resp.OK {
		t.Fatalf("expected OK with approved plan, got error: %v", resp.Error)
	}
	if resp.Payload != "approved-execution" {
		t.Errorf("Payload = %v, want %q", resp.Payload, "approved-execution")
	}
}

func TestDispatch_PlanNilPlanner_SkipsPlanCheck(t *testing.T) {
	audit := &mockAuditLogger{}
	d := New(
		&mockAuthenticator{actor: &types.Actor{Type: "web", ID: "browser_abc"}},
		&mockPluginRegistry{plugin: &PluginEntry{ID: "test-plugin", Enabled: true}},
		&mockPermissionChecker{},
		nil, /* no planner configured */
		&mockExecutor{result: "direct"},
		audit,
		&mockTopology{},
		"node_local",
	)

	req := makePayload()
	resp := d.Dispatch(&req)

	if !resp.OK {
		t.Fatalf("expected OK when no planner, got error: %v", resp.Error)
	}
}

func TestDispatch_PlanCreationFailure(t *testing.T) {
	audit := &mockAuditLogger{}
	d := New(
		&mockAuthenticator{actor: &types.Actor{Type: "web", ID: "browser_abc"}},
		&mockPluginRegistry{plugin: &PluginEntry{ID: "test-plugin", Enabled: true}},
		&mockPermissionChecker{},
		&mockPlanner{requiresPlan: true, err: errors.New("store full")},
		&mockExecutor{result: "ok"},
		audit,
		&mockTopology{},
		"node_local",
	)

	req := makePayload()
	req.Capability = "plugin.install"
	resp := d.Dispatch(&req)

	if resp.OK {
		t.Fatal("expected failure on plan creation error")
	}
	if resp.Error == nil || resp.Error.Code != protocol.ErrCodePlanFailed {
		t.Errorf("expected code %s, got %v", protocol.ErrCodePlanFailed, resp.Error)
	}
}

// --- Plan approval gate tests ---

func TestDispatch_HighRiskWithApprovedPlan_Executes(t *testing.T) {
	audit := &mockAuditLogger{}
	d := New(
		&mockAuthenticator{actor: &types.Actor{Type: "web", ID: "browser_abc"}},
		&mockPluginRegistry{plugin: &PluginEntry{ID: "test-plugin", Enabled: true}},
		&mockPermissionChecker{},
		&mockPlanner{requiresPlan: true, planID: "plan_001", planState: "approved"},
		&mockExecutor{result: "approved-execution"},
		audit,
		&mockTopology{},
		"node_local",
	)

	req := makePayload()
	req.Capability = "session.history.clear.execute"
	req.PlanID = "plan_001"
	resp := d.Dispatch(&req)

	if !resp.OK {
		t.Fatalf("expected OK with approved plan, got error: %v", resp.Error)
	}
	if resp.Payload != "approved-execution" {
		t.Errorf("Payload = %v, want %q", resp.Payload, "approved-execution")
	}
	// Audit should have at least 2 entries (plan approved + execution success)
	if audit.Count() < 2 {
		t.Errorf("expected at least 2 audit entries, got %d", audit.Count())
	}
	// Check that one of the entries records plan approval
	foundPlanApproved := false
	for _, e := range audit.entries {
		if e.allowed && e.detail == "plan approved: plan_001" {
			foundPlanApproved = true
			break
		}
	}
	if !foundPlanApproved {
		t.Error("expected an audit entry with detail 'plan approved: plan_001'")
	}
}

func TestDispatch_HighRiskWithDeniedPlan_ReturnsApprovalDenied(t *testing.T) {
	audit := &mockAuditLogger{}
	d := New(
		&mockAuthenticator{actor: &types.Actor{Type: "web", ID: "browser_abc"}},
		&mockPluginRegistry{plugin: &PluginEntry{ID: "test-plugin", Enabled: true}},
		&mockPermissionChecker{},
		&mockPlanner{requiresPlan: true, planID: "plan_001", planState: "denied"},
		&mockExecutor{result: "ok"},
		audit,
		&mockTopology{},
		"node_local",
	)

	req := makePayload()
	req.Capability = "session.history.clear.execute"
	req.PlanID = "plan_001"
	resp := d.Dispatch(&req)

	if resp.OK {
		t.Fatal("expected failure for denied plan")
	}
	if resp.Error == nil || resp.Error.Code != protocol.ErrCodeApprovalDenied {
		t.Errorf("expected code %s, got %v", protocol.ErrCodeApprovalDenied, resp.Error)
	}
}

func TestDispatch_HighRiskWithPendingPlan_ReturnsApprovalRequired(t *testing.T) {
	audit := &mockAuditLogger{}
	d := New(
		&mockAuthenticator{actor: &types.Actor{Type: "web", ID: "browser_abc"}},
		&mockPluginRegistry{plugin: &PluginEntry{ID: "test-plugin", Enabled: true}},
		&mockPermissionChecker{},
		&mockPlanner{requiresPlan: true, planID: "plan_001", planState: "pending"},
		&mockExecutor{result: "ok"},
		audit,
		&mockTopology{},
		"node_local",
	)

	req := makePayload()
	req.Capability = "session.history.clear.execute"
	req.PlanID = "plan_001"
	resp := d.Dispatch(&req)

	if resp.OK {
		t.Fatal("expected failure for pending plan")
	}
	if resp.Error == nil || resp.Error.Code != protocol.ErrCodeApprovalRequired {
		t.Errorf("expected code %s, got %v", protocol.ErrCodeApprovalRequired, resp.Error)
	}
}

func TestDispatch_HighRiskPlanNotFound_ReturnsPlanRequired(t *testing.T) {
	audit := &mockAuditLogger{}
	d := New(
		&mockAuthenticator{actor: &types.Actor{Type: "web", ID: "browser_abc"}},
		&mockPluginRegistry{plugin: &PluginEntry{ID: "test-plugin", Enabled: true}},
		&mockPermissionChecker{},
		&mockPlanner{requiresPlan: true, planState: "not_found"},
		&mockExecutor{result: "ok"},
		audit,
		&mockTopology{},
		"node_local",
	)

	req := makePayload()
	req.Capability = "session.history.clear.execute"
	req.PlanID = "plan_missing"
	resp := d.Dispatch(&req)

	if resp.OK {
		t.Fatal("expected failure for missing plan")
	}
	if resp.Error == nil || resp.Error.Code != protocol.ErrCodePlanRequired {
		t.Errorf("expected code %s, got %v", protocol.ErrCodePlanRequired, resp.Error)
	}
}

// --- Helpers ---

func createSuccessDispatcher(audit *mockAuditLogger, localNodeID types.NodeID) *Dispatcher {
	return New(
		&mockAuthenticator{actor: &types.Actor{Type: "web", ID: "browser_abc"}},
		&mockPluginRegistry{plugin: &PluginEntry{ID: "test-plugin", Enabled: true}},
		&mockPermissionChecker{},
		nil, /* no planner */
		&mockExecutor{result: map[string]interface{}{"status": "ok"}},
		audit,
		&mockTopology{},
		localNodeID,
	)
}
