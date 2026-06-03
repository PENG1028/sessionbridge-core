package plan

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/user/sessionnode/go-core/pkg/types"
)

// DefaultHighRiskCaps is the default set of capabilities that require Plan Before Apply.
var DefaultHighRiskCaps = []string{
	"session.history.clear.execute",
	"session.history.setPolicy",
	"fs.write",
	"fs.remove",
}

// Manager implements dispatcher.Planner. It wraps PlanStore and enforces
// Plan Before Apply for a configured set of high-risk capabilities.
type Manager struct {
	store    *PlanStore
	highRisk map[string]bool
	nextID   atomic.Int64
	planTTL  time.Duration
}

// NewManager creates a PlanManager with the given store and high-risk capabilities list.
func NewManager(store *PlanStore, highRisk []string) *Manager {
	hr := make(map[string]bool, len(highRisk))
	for _, c := range highRisk {
		hr[c] = true
	}
	return &Manager{
		store:    store,
		highRisk: hr,
		planTTL:  5 * time.Minute,
	}
}

// RequiresPlan returns true if the capability is in the high-risk set.
func (m *Manager) RequiresPlan(capability string) bool {
	return m.highRisk[capability]
}

// CreatePlan creates a pending plan for the request and returns the plan ID.
func (m *Manager) CreatePlan(req *types.CapabilityRequest) (string, error) {
	id := fmt.Sprintf("plan_%d", m.nextID.Add(1))

	summary := fmt.Sprintf("Execute %s", req.Capability)
	desc := fmt.Sprintf("Requested by %s/%s for plugin %s", req.Actor.Type, req.Actor.ID, req.PluginID)

	details := map[string]interface{}{
		"capability": req.Capability,
		"pluginId":   string(req.PluginID),
		"actorType":  req.Actor.Type,
		"actorId":    req.Actor.ID,
	}
	if len(req.Payload) > 0 {
		details["hasPayload"] = true
	}
	if req.TargetNodeID != "" {
		details["targetNodeId"] = string(req.TargetNodeID)
	}

	p := NewPlan(id, req.Capability, summary, desc, details, string(req.Actor.ID), m.planTTL)
	m.store.Create(p)
	return id, nil
}

// GetPlan retrieves a plan by ID from the store.
func (m *Manager) GetPlan(planID string) (*Plan, error) {
	p := m.store.Get(planID)
	if p == nil {
		return nil, fmt.Errorf("plan not found: %s", planID)
	}
	return p, nil
}

// ApprovePlan transitions a plan from pending to approved state.
func (m *Manager) ApprovePlan(planID, approvedBy string) error {
	p, err := m.GetPlan(planID)
	if err != nil {
		return err
	}
	return p.Approve(approvedBy)
}

// DenyPlan transitions a plan from pending to denied state.
func (m *Manager) DenyPlan(planID, deniedBy, reason string) error {
	p, err := m.GetPlan(planID)
	if err != nil {
		return err
	}
	return p.Deny(deniedBy, reason)
}

// ValidatePlan returns nil if the plan exists and is approved (ready for execution).
// Returns an error describing why the plan cannot be executed.
func (m *Manager) ValidatePlan(planID string) error {
	p, err := m.GetPlan(planID)
	if err != nil {
		return err
	}
	if p.IsExpired() {
		// Auto-transition to expired state
		p.Transition(StateExpired)
		return fmt.Errorf("plan %s has expired", planID)
	}
	if p.State == StateDenied {
		return fmt.Errorf("plan %s was denied", planID)
	}
	if p.State == StatePending {
		return fmt.Errorf("plan %s is pending approval", planID)
	}
	if p.State == StateExpired {
		return fmt.Errorf("plan %s has expired", planID)
	}
	if p.State != StateApproved {
		return fmt.Errorf("plan %s is not in an executable state: %s", planID, p.State)
	}
	return nil
}
