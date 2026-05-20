package plan

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/user/sessionnode/go-core/pkg/types"
)

func TestManager_RequiresPlan_HighRisk(t *testing.T) {
	m := NewManager(NewPlanStore(), DefaultHighRiskCaps)
	for _, cap := range DefaultHighRiskCaps {
		if !m.RequiresPlan(cap) {
			t.Errorf("expected RequiresPlan(%q) = true", cap)
		}
	}
}

func TestManager_RequiresPlan_SafeCap(t *testing.T) {
	m := NewManager(NewPlanStore(), DefaultHighRiskCaps)
	safeCaps := []string{"session.create", "stream.write", "session.list", "system.info"}
	for _, cap := range safeCaps {
		if m.RequiresPlan(cap) {
			t.Errorf("expected RequiresPlan(%q) = false", cap)
		}
	}
}

func TestManager_RequiresPlan_EmptyList(t *testing.T) {
	m := NewManager(NewPlanStore(), nil)
	if m.RequiresPlan("session.history.clear.execute") {
		t.Error("expected false for empty high-risk list")
	}
}

func TestManager_CreatePlan(t *testing.T) {
	store := NewPlanStore()
	m := NewManager(store, DefaultHighRiskCaps)

	req := &types.CapabilityRequest{
		RequestID:  "req_001",
		PluginID:   "shell",
		Capability: "session.history.clear.execute",
		Actor:      types.Actor{Type: "user", ID: "alice"},
	}

	planID, err := m.CreatePlan(req)
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if planID == "" {
		t.Fatal("expected non-empty plan ID")
	}
	if len(store.List()) != 1 {
		t.Fatalf("expected 1 plan in store, got %d", len(store.List()))
	}

	p := store.Get(planID)
	if p == nil {
		t.Fatalf("plan %s not found in store", planID)
	}
	if p.Capability != "session.history.clear.execute" {
		t.Errorf("Capability = %q", p.Capability)
	}
	if p.State != StatePending {
		t.Errorf("State = %q, want %q", p.State, StatePending)
	}
}

func TestManager_CreatePlan_IncrementsID(t *testing.T) {
	store := NewPlanStore()
	m := NewManager(store, DefaultHighRiskCaps)

	req := &types.CapabilityRequest{
		RequestID:  "req_001",
		PluginID:   "shell",
		Capability: "fs.write",
		Actor:      types.Actor{Type: "user", ID: "bob"},
	}

	id1, _ := m.CreatePlan(req)
	id2, _ := m.CreatePlan(req)
	if id1 == id2 {
		t.Error("expected different plan IDs")
	}

	p1 := store.Get(id1)
	p2 := store.Get(id2)
	if p1 == nil || p2 == nil {
		t.Fatal("expected both plans in store")
	}
}

func TestManager_CreatePlan_WithPayload(t *testing.T) {
	store := NewPlanStore()
	m := NewManager(store, DefaultHighRiskCaps)

	payload := json.RawMessage(`{"sessionId":"sess_001","streams":["stdout"]}`)
	req := &types.CapabilityRequest{
		RequestID:  "req_002",
		PluginID:   "shell",
		Capability: "session.history.clear.execute",
		Payload:    payload,
		Actor:      types.Actor{Type: "user", ID: "carol"},
	}

	planID, _ := m.CreatePlan(req)
	p := store.Get(planID)
	if p == nil {
		t.Fatal("plan not found")
	}
	if p.Details["hasPayload"] != true {
		t.Error("expected hasPayload=true in details")
	}
}

func TestManager_CreatePlan_TTL(t *testing.T) {
	store := NewPlanStore()
	m := NewManager(store, DefaultHighRiskCaps)

	req := &types.CapabilityRequest{
		Capability: "fs.remove",
		Actor:      types.Actor{ID: "dave"},
	}

	planID, _ := m.CreatePlan(req)
	p := store.Get(planID)

	if p.ExpiresAt <= p.CreatedAt {
		t.Error("expected ExpiresAt > CreatedAt")
	}
	expectedTTL := int64(5 * time.Minute.Milliseconds())
	if p.ExpiresAt-p.CreatedAt > expectedTTL+1000 {
		t.Errorf("TTL too large: %d ms", p.ExpiresAt-p.CreatedAt)
	}
}
