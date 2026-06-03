package plan

import (
	"fmt"
	"testing"
	"time"
)

func TestNewPlan_DefaultTTL(t *testing.T) {
	p := NewPlan("p1", "test.cap", "summary", "desc", nil, "user", 0)
	if p.State != StatePending {
		t.Errorf("state = %q, want %q", p.State, StatePending)
	}
	if p.ExpiresAt == 0 {
		t.Error("ExpiresAt should be set")
	}
	if p.CreatedBy != "user" {
		t.Errorf("CreatedBy = %q", p.CreatedBy)
	}
	if p.ID != "p1" {
		t.Errorf("ID = %q", p.ID)
	}
}

func TestNewPlan_CustomTTL(t *testing.T) {
	ttl := 10 * time.Minute
	p := NewPlan("p2", "test.cap", "s", "d", nil, "user", ttl)
	expected := p.CreatedAt + ttl.Milliseconds()
	// Allow 1ms tolerance for clock ticks
	if diff := p.ExpiresAt - expected; diff > 1 || diff < -1 {
		t.Errorf("ExpiresAt = %d, want ~%d", p.ExpiresAt, expected)
	}
}

func TestPlan_Transition_Valid(t *testing.T) {
	p := NewPlan("p1", "test.cap", "s", "d", nil, "user", 0)

	if err := p.Approve("admin"); err != nil {
		t.Fatalf("Approve failed: %v", err)
	}
	if p.State != StateApproved {
		t.Errorf("state = %q, want %q", p.State, StateApproved)
	}
	if p.ApprovedBy != "admin" {
		t.Errorf("ApprovedBy = %q", p.ApprovedBy)
	}

	if err := p.MarkExecuted("ok"); err != nil {
		t.Fatalf("MarkExecuted failed: %v", err)
	}
	if p.State != StateExecuted {
		t.Errorf("state = %q, want %q", p.State, StateExecuted)
	}
	if p.Result != "ok" {
		t.Errorf("Result = %v, want %v", p.Result, "ok")
	}
}

func TestPlan_Transition_DenyFlow(t *testing.T) {
	p := NewPlan("p1", "test.cap", "s", "d", nil, "user", 0)

	if err := p.Deny("admin", "not needed"); err != nil {
		t.Fatalf("Deny failed: %v", err)
	}
	if p.State != StateDenied {
		t.Errorf("state = %q, want %q", p.State, StateDenied)
	}
	if p.DeniedBy != "admin" {
		t.Errorf("DeniedBy = %q", p.DeniedBy)
	}
	if p.DenyReason != "not needed" {
		t.Errorf("DenyReason = %q", p.DenyReason)
	}
}

func TestPlan_Transition_FailFlow(t *testing.T) {
	p := NewPlan("p1", "test.cap", "s", "d", nil, "user", 0)
	p.Approve("admin")

	if err := p.MarkFailed("timeout"); err != nil {
		t.Fatalf("MarkFailed failed: %v", err)
	}
	if p.State != StateFailed {
		t.Errorf("state = %q, want %q", p.State, StateFailed)
	}
	if p.Error != "timeout" {
		t.Errorf("Error = %q", p.Error)
	}
}

func TestPlan_Transition_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*Plan)
		attempt func(*Plan) error
	}{
		{"denied→approved", func(p *Plan) { p.State = StateDenied }, func(p *Plan) error { return p.Approve("admin") }},
		{"denied→denied", func(p *Plan) { p.State = StateDenied }, func(p *Plan) error { return p.Deny("admin", "no") }},
		{"executed→approved", func(p *Plan) { p.State = StateExecuted }, func(p *Plan) error { return p.Approve("admin") }},
		{"expired→approved", func(p *Plan) { p.State = StateExpired }, func(p *Plan) error { return p.Approve("admin") }},
		{"approved→approved", func(p *Plan) { p.State = StateApproved }, func(p *Plan) error { return p.Approve("admin") }},
		{"pending→executed", func(p *Plan) { p.State = StatePending }, func(p *Plan) error { return p.MarkExecuted("x") }},
		{"pending→failed", func(p *Plan) { p.State = StatePending }, func(p *Plan) error { return p.MarkFailed("x") }},
		{"failed→approved", func(p *Plan) { p.State = StateFailed }, func(p *Plan) error { return p.Approve("admin") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPlan("p1", "test.cap", "s", "d", nil, "user", time.Hour)
			tt.setup(p)
			if err := tt.attempt(p); err == nil {
				t.Errorf("expected error for transition")
			}
		})
	}
}

func TestPlan_IsExpired(t *testing.T) {
	p := NewPlan("p1", "test.cap", "s", "d", nil, "user", 0)
	// Should not be expired immediately
	if p.IsExpired() {
		t.Error("fresh plan should not be expired")
	}
}

func TestPlan_IsTerminal(t *testing.T) {
	tests := []struct {
		state string
		want  bool
	}{
		{StatePending, false},
		{StateApproved, false},
		{StateDenied, true},
		{StateExecuted, true},
		{StateExpired, true},
		{StateFailed, true},
	}
	for _, tt := range tests {
		p := NewPlan("p1", "test.cap", "s", "d", nil, "user", time.Hour)
		p.State = tt.state
		if got := p.IsTerminal(); got != tt.want {
			t.Errorf("IsTerminal(%s) = %v, want %v", tt.state, got, tt.want)
		}
	}
}

func TestPlan_IsActionable(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Plan)
		want  bool
	}{
		{"pending not expired", func(p *Plan) { p.ExpiresAt = time.Now().Add(time.Hour).UnixMilli() }, true},
		{"pending expired", func(p *Plan) { p.ExpiresAt = time.Now().Add(-time.Hour).UnixMilli() }, false},
		{"approved", func(p *Plan) { p.State = StateApproved; p.ExpiresAt = time.Now().Add(time.Hour).UnixMilli() }, false},
		{"denied", func(p *Plan) { p.State = StateDenied; p.ExpiresAt = time.Now().Add(time.Hour).UnixMilli() }, false},
		{"executed", func(p *Plan) { p.State = StateExecuted; p.ExpiresAt = time.Now().Add(time.Hour).UnixMilli() }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPlan("p1", "test.cap", "s", "d", nil, "user", time.Hour)
			tt.setup(p)
			if got := p.IsActionable(); got != tt.want {
				t.Errorf("IsActionable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlan_Transition_SetsUpdatedAt(t *testing.T) {
	p := NewPlan("p1", "test.cap", "s", "d", nil, "user", time.Hour)
	before := p.UpdatedAt
	time.Sleep(time.Millisecond)
	p.Approve("admin")
	if p.UpdatedAt <= before {
		t.Error("UpdatedAt should advance on transition")
	}
}

// --- PlanStore tests ---

func TestPlanStore_CreateAndGet(t *testing.T) {
	s := NewPlanStore()
	p := NewPlan("p1", "test.cap", "s", "d", nil, "user", 0)
	s.Create(p)

	got := s.Get("p1")
	if got == nil {
		t.Fatal("expected plan, got nil")
	}
	if got.ID != "p1" {
		t.Errorf("ID = %q", got.ID)
	}
}

func TestPlanStore_Get_NotFound(t *testing.T) {
	s := NewPlanStore()
	if got := s.Get("nonexistent"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestPlanStore_List(t *testing.T) {
	s := NewPlanStore()
	s.Create(NewPlan("p1", "c1", "s", "d", nil, "u", 0))
	s.Create(NewPlan("p2", "c2", "s", "d", nil, "u", 0))
	s.Create(NewPlan("p3", "c3", "s", "d", nil, "u", 0))

	plans := s.List()
	if len(plans) != 3 {
		t.Errorf("len = %d, want 3", len(plans))
	}
}

func TestPlanStore_Delete(t *testing.T) {
	s := NewPlanStore()
	s.Create(NewPlan("p1", "c1", "s", "d", nil, "u", 0))
	s.Delete("p1")

	if got := s.Get("p1"); got != nil {
		t.Error("plan should be nil after delete")
	}
}

func TestPlanStore_Pending(t *testing.T) {
	s := NewPlanStore()
	p1 := NewPlan("p1", "c1", "s", "d", nil, "u", time.Hour)
	p2 := NewPlan("p2", "c2", "s", "d", nil, "u", time.Hour)
	p3 := NewPlan("p3", "c3", "s", "d", nil, "u", time.Hour)
	p2.Approve("admin")
	p3.Deny("admin", "no")

	s.Create(p1)
	s.Create(p2) // approved — should not appear
	s.Create(p3) // denied — should not appear

	pending := s.Pending(false)
	if len(pending) != 1 {
		t.Errorf("pending count = %d, want 1", len(pending))
	}
	if len(pending) > 0 && pending[0].ID != "p1" {
		t.Errorf("pending[0].ID = %q, want %q", pending[0].ID, "p1")
	}
}

func TestPlanStore_Pending_FilterExpired(t *testing.T) {
	s := NewPlanStore()
	p1 := NewPlan("p1", "c1", "s", "d", nil, "u", time.Hour) // not expired
	p2 := NewPlan("p2", "c2", "s", "d", nil, "u", time.Hour) // expired by manual override
	p2.ExpiresAt = time.Now().Add(-time.Hour).UnixMilli()

	s.Create(p1)
	s.Create(p2)

	pending := s.Pending(true)
	if len(pending) != 1 {
		t.Errorf("pending count = %d, want 1 (expired filtered)", len(pending))
	}
	if len(pending) > 0 && pending[0].ID != "p1" {
		t.Errorf("pending[0].ID = %q, want %q", pending[0].ID, "p1")
	}
}

func TestPlanStore_AutoExpire(t *testing.T) {
	s := NewPlanStore()
	s.Create(NewPlan("p1", "c1", "s", "d", nil, "u", time.Hour)) // valid
	p2 := NewPlan("p2", "c2", "s", "d", nil, "u", time.Hour)     // expired by override
	p2.ExpiresAt = time.Now().Add(-time.Hour).UnixMilli()
	s.Create(p2)

	// Also add a non-pending expired plan (should not be auto-expired)
	p3 := NewPlan("p3", "c3", "s", "d", nil, "u", time.Hour)
	p3.ExpiresAt = time.Now().Add(-time.Hour).UnixMilli()
	p3.State = StateDenied
	s.Create(p3)

	count := s.AutoExpire()
	if count != 1 {
		t.Errorf("autoExpire count = %d, want 1", count)
	}

	if s.Get("p1").State != StatePending {
		t.Error("p1 should still be pending")
	}
	if s.Get("p2").State != StateExpired {
		t.Error("p2 should be expired")
	}
	if s.Get("p3").State != StateDenied {
		t.Error("p3 should still be denied")
	}
}

func TestPlanStore_ConcurrentAccess(t *testing.T) {
	s := NewPlanStore()
	done := make(chan struct{})

	go func() {
		for i := 0; i < 50; i++ {
			s.Create(NewPlan(fmt.Sprintf("p%d", i), "c", "s", "d", nil, "u", 0))
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 50; i < 100; i++ {
			s.Create(NewPlan(fmt.Sprintf("p%d", i), "c", "s", "d", nil, "u", 0))
		}
		done <- struct{}{}
	}()

	<-done
	<-done

	if len(s.List()) != 100 {
		t.Errorf("expected 100 plans, got %d", len(s.List()))
	}
}

func TestPlanStore_AutoExpireIdempotent(t *testing.T) {
	s := NewPlanStore()
	p1 := NewPlan("p1", "c1", "s", "d", nil, "u", time.Hour)
	p1.ExpiresAt = time.Now().Add(-time.Hour).UnixMilli()
	s.Create(p1)

	s.AutoExpire()
	if s.Get("p1").State != StateExpired {
		t.Fatal("p1 should be expired after first call")
	}

	// Second call should not change count
	count := s.AutoExpire()
	if count != 0 {
		t.Errorf("second AutoExpire count = %d, want 0", count)
	}
}

func TestPlan_FullLifecycle(t *testing.T) {
	p := NewPlan("p_lifecycle", "plugin.install", "Install foobar v2",
		"Install the foobar plugin version 2.0.0", nil, "operator", time.Hour)

	// pending → actionable
	if !p.IsActionable() {
		t.Error("pending plan should be actionable")
	}
	if p.IsTerminal() {
		t.Error("pending plan should not be terminal")
	}

	// pending → approved
	if err := p.Approve("admin"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if p.ApprovedBy != "admin" {
		t.Errorf("ApprovedBy = %q", p.ApprovedBy)
	}
	if p.IsActionable() {
		t.Error("approved plan should not be actionable")
	}

	// approved → executed
	if err := p.MarkExecuted(map[string]interface{}{"status": "done"}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if p.State != StateExecuted {
		t.Errorf("state = %q", p.State)
	}
	if !p.IsTerminal() {
		t.Error("executed plan should be terminal")
	}
}
