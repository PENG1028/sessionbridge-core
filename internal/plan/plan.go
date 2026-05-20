package plan

import (
	"fmt"
	"sync"
	"time"
)

// Plan state constants.
const (
	StatePending  = "pending"   // plan created, awaiting approval
	StateApproved = "approved"  // approved by authorized actor
	StateDenied   = "denied"    // explicitly denied
	StateExecuted = "executed"  // plan was executed
	StateExpired  = "expired"   // ttl passed before approval
	StateFailed   = "failed"    // execution failed
)

// validTransitions for plan state machine.
var validTransitions = map[string]map[string]bool{
	StatePending:  {StateApproved: true, StateDenied: true, StateExpired: true},
	StateApproved: {StateExecuted: true, StateFailed: true, StateExpired: true},
	StateDenied:   {},
	StateExecuted: {},
	StateExpired:  {},
	StateFailed:   {},
}

// Plan describes a high-risk operation that requires human approval before execution.
type Plan struct {
	ID          string                 `json:"id"`
	Capability  string                 `json:"capability"`
	Summary     string                 `json:"summary"`
	Description string                 `json:"description"`
	Details     map[string]interface{} `json:"details,omitempty"`
	State       string                 `json:"state"`
	CreatedAt   int64                  `json:"createdAt"`
	UpdatedAt   int64                  `json:"updatedAt"`
	ExpiresAt   int64                  `json:"expiresAt"`
	CreatedBy   string                 `json:"createdBy"`
	ApprovedBy  string                 `json:"approvedBy,omitempty"`
	DeniedBy    string                 `json:"deniedBy,omitempty"`
	DenyReason  string                 `json:"denyReason,omitempty"`
	Result      interface{}            `json:"result,omitempty"`
	Error       string                 `json:"error,omitempty"`
}

// NewPlan creates a new Plan with the given parameters and a default TTL of 5 minutes.
func NewPlan(id, capability, summary, description string, details map[string]interface{}, createdBy string, ttl time.Duration) *Plan {
	now := time.Now()
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &Plan{
		ID:          id,
		Capability:  capability,
		Summary:     summary,
		Description: description,
		Details:     details,
		State:       StatePending,
		CreatedAt:   now.UnixMilli(),
		UpdatedAt:   now.UnixMilli(),
		ExpiresAt:   now.Add(ttl).UnixMilli(),
		CreatedBy:   createdBy,
	}
}

// Transition validates and applies a state transition.
func (p *Plan) Transition(newState string) error {
	allowed, ok := validTransitions[p.State]
	if !ok {
		return fmt.Errorf("unknown current state: %s", p.State)
	}
	if !allowed[newState] {
		return fmt.Errorf("invalid plan transition: %s → %s", p.State, newState)
	}
	p.State = newState
	p.UpdatedAt = time.Now().UnixMilli()
	return nil
}

// Approve transitions the plan from pending to approved.
func (p *Plan) Approve(approvedBy string) error {
	if err := p.Transition(StateApproved); err != nil {
		return err
	}
	p.ApprovedBy = approvedBy
	return nil
}

// Deny transitions the plan from pending to denied.
func (p *Plan) Deny(deniedBy, reason string) error {
	if err := p.Transition(StateDenied); err != nil {
		return err
	}
	p.DeniedBy = deniedBy
	p.DenyReason = reason
	return nil
}

// MarkExecuted transitions the plan from approved to executed.
func (p *Plan) MarkExecuted(result interface{}) error {
	if err := p.Transition(StateExecuted); err != nil {
		return err
	}
	p.Result = result
	return nil
}

// MarkFailed transitions the plan from approved to failed.
func (p *Plan) MarkFailed(errMsg string) error {
	if err := p.Transition(StateFailed); err != nil {
		return err
	}
	p.Error = errMsg
	return nil
}

// IsExpired returns true if the current time is past the plan's expiration.
func (p *Plan) IsExpired() bool {
	return time.Now().UnixMilli() > p.ExpiresAt
}

// IsTerminal returns true if the plan is in a final state.
func (p *Plan) IsTerminal() bool {
	return p.State == StateExecuted || p.State == StateDenied ||
		p.State == StateExpired || p.State == StateFailed
}

// IsActionable returns true if the plan can still be approved or denied.
func (p *Plan) IsActionable() bool {
	if p.IsExpired() {
		return false
	}
	return p.State == StatePending
}

// PlanStore is a thread-safe in-memory store for plans.
type PlanStore struct {
	mu     sync.RWMutex
	plans  map[string]*Plan
	nextID int64
}

// NewPlanStore creates an empty PlanStore.
func NewPlanStore() *PlanStore {
	return &PlanStore{
		plans: make(map[string]*Plan),
	}
}

// Create stores a new plan and returns its ID.
func (s *PlanStore) Create(p *Plan) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plans[p.ID] = p
}

// Get retrieves a plan by ID. Returns nil if not found.
func (s *PlanStore) Get(id string) *Plan {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.plans[id]
}

// List returns all plans.
func (s *PlanStore) List() []*Plan {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Plan, 0, len(s.plans))
	for _, p := range s.plans {
		out = append(out, p)
	}
	return out
}

// Delete removes a plan by ID.
func (s *PlanStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.plans, id)
}

// Pending returns all plans in pending state, optionally filtering expired ones.
func (s *PlanStore) Pending(filterExpired bool) []*Plan {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Plan
	for _, p := range s.plans {
		if p.State == StatePending {
			if filterExpired && p.IsExpired() {
				continue
			}
			out = append(out, p)
		}
	}
	return out
}

// AutoExpire transitions all expired pending plans to expired state.
// Returns the number of plans that were expired.
func (s *PlanStore) AutoExpire() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, p := range s.plans {
		if p.State == StatePending && p.IsExpired() {
			p.State = StateExpired
			p.UpdatedAt = time.Now().UnixMilli()
			count++
		}
	}
	return count
}
