package run

import (
	"sync"
	"testing"

	"github.com/user/sessionnode/go-core/pkg/types"
)

func TestStore_Create(t *testing.T) {
	s := NewStore()
	r := s.Create(&Run{
		NodeID:   "node-1",
		Kind:     KindTerminal,
		Label:    "test terminal",
		PluginID: "terminal",
		Policy:   DefaultPolicy(),
	})

	if r.RunID == "" {
		t.Fatal("expected runId to be set")
	}
	if len(r.RunID) < 4 || r.RunID[:4] != "run_" {
		t.Errorf("runId should start with 'run_', got %q", r.RunID)
	}
	if r.State != StateRunning {
		t.Errorf("State = %q, want %q", r.State, StateRunning)
	}
	if r.CreatedAt == 0 {
		t.Error("CreatedAt should be set")
	}
	if r.UpdatedAt == 0 {
		t.Error("UpdatedAt should be set")
	}
	if r.Metadata == nil {
		t.Error("Metadata should be initialized to empty map")
	}
}

func TestStore_Get(t *testing.T) {
	s := NewStore()
	r := s.Create(&Run{
		NodeID:   "node-1",
		Kind:     KindTerminal,
		PluginID: "terminal",
		Metadata: map[string]string{"key": "val"},
	})

	got := s.Get(r.RunID)
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.RunID != r.RunID {
		t.Errorf("RunID = %q, want %q", got.RunID, r.RunID)
	}
	if got.Metadata["key"] != "val" {
		t.Error("Metadata not preserved")
	}

	// Verify copy isolation
	got.Metadata["key"] = "modified"
	got2 := s.Get(r.RunID)
	if got2.Metadata["key"] != "val" {
		t.Error("Get should return a copy of Metadata")
	}
}

func TestStore_Get_NotFound(t *testing.T) {
	s := NewStore()
	if got := s.Get("nonexistent"); got != nil {
		t.Error("Get should return nil for unknown run")
	}
}

func TestStore_List(t *testing.T) {
	s := NewStore()
	s.Create(&Run{NodeID: "n1", Kind: KindTerminal, PluginID: "terminal"})
	s.Create(&Run{NodeID: "n1", Kind: KindProcess, PluginID: "claude-code"})
	s.Create(&Run{NodeID: "n2", Kind: KindTerminal, PluginID: "terminal"})

	all := s.List("", "", "")
	if len(all) != 3 {
		t.Fatalf("expected 3 runs, got %d", len(all))
	}

	terminals := s.List(KindTerminal, "", "")
	if len(terminals) != 2 {
		t.Fatalf("expected 2 terminals, got %d", len(terminals))
	}

	filtered := s.List(KindTerminal, "terminal", "")
	if len(filtered) != 2 {
		t.Fatalf("expected 2, got %d", len(filtered))
	}

	byPlugin := s.List("", "claude-code", "")
	if len(byPlugin) != 1 {
		t.Fatalf("expected 1, got %d", len(byPlugin))
	}

	byState := s.List("", "", StateRunning)
	if len(byState) != 3 {
		t.Fatalf("expected 3 running, got %d", len(byState))
	}

	empty := s.List(KindService, "", "")
	if len(empty) != 0 {
		t.Errorf("expected 0, got %d", len(empty))
	}
	if empty == nil {
		t.Error("List should return empty slice, not nil")
	}
}

func TestStore_UpdateState(t *testing.T) {
	s := NewStore()
	r := s.Create(&Run{NodeID: "n1", Kind: KindTerminal})

	s.UpdateState(r.RunID, StateExited)

	got := s.Get(r.RunID)
	if got.State != StateExited {
		t.Errorf("State = %q, want %q", got.State, StateExited)
	}
	if got.UpdatedAt < r.UpdatedAt {
		t.Error("UpdatedAt should be >= original")
	}
}

func TestStore_UpdatePolicy(t *testing.T) {
	s := NewStore()
	r := s.Create(&Run{NodeID: "n1", Kind: KindTerminal, Policy: DefaultPolicy()})

	newP := DefaultPolicy()
	newP.PersistHistory = false
	err := s.UpdatePolicy(r.RunID, newP)
	if err != nil {
		t.Fatalf("UpdatePolicy failed: %v", err)
	}

	got := s.Get(r.RunID)
	if got.Policy.PersistHistory != false {
		t.Error("policy not updated")
	}
}

func TestStore_UpdatePolicy_AcceptsRestartRestore(t *testing.T) {
	s := NewStore()
	r := s.Create(&Run{NodeID: "n1", Kind: KindTerminal})

	err := s.UpdatePolicy(r.RunID, Policy{
		OnDisconnect:   OnDisconnectKeepRunning,
		OnCoreShutdown: OnCoreShutdownTerminate,
		RestartRestore: true,
	})
	if err != nil {
		t.Fatalf("expected restartRestore=true to be accepted, got: %v", err)
	}
	got := s.Get(r.RunID)
	if !got.Policy.RestartRestore {
		t.Error("RestartRestore should be true")
	}
}

func TestStore_UpdatePolicy_RejectsUnsupportedOnCoreShutdown(t *testing.T) {
	s := NewStore()
	r := s.Create(&Run{NodeID: "n1", Kind: KindTerminal})

	err := s.UpdatePolicy(r.RunID, Policy{
		OnCoreShutdown: "leave_running",
	})
	if err == nil {
		t.Fatal("expected error for unsupported onCoreShutdown")
	}
}

func TestStore_ConcurrentAccess(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	n := 50

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			s.Create(&Run{NodeID: "n1", Kind: KindTerminal})
		}(i)
	}
	wg.Wait()

	if s.Count() != n {
		t.Errorf("Count = %d, want %d", s.Count(), n)
	}

	all := s.List("", "", "")
	if len(all) != n {
		t.Errorf("List length = %d, want %d", len(all), n)
	}
}

func TestStore_Delete(t *testing.T) {
	s := NewStore()
	r := s.Create(&Run{NodeID: "n1", Kind: KindTerminal})

	if !s.Delete(r.RunID) {
		t.Error("Delete should return true for existing run")
	}
	if s.Get(r.RunID) != nil {
		t.Error("Get after Delete should return nil")
	}
	if s.Delete(r.RunID) {
		t.Error("Delete should return false for already-deleted run")
	}
}

func TestStore_SaveProcessRef(t *testing.T) {
	s := NewStore()
	r := s.Create(&Run{NodeID: "n1", Kind: KindTerminal})

	sid := types.SessionID("sess_proc_1234_5678")
	s.SaveProcessRef(r.RunID, sid, StateRunning)

	got := s.Get(r.RunID)
	if got.SessionID != sid {
		t.Errorf("SessionID = %q, want %q", got.SessionID, sid)
	}
	if got.ProcessID != sid {
		t.Errorf("ProcessID = %q, want %q", got.ProcessID, sid)
	}
	if got.UpdatedAt < r.UpdatedAt {
		t.Error("UpdatedAt should be >= original")
	}
}

func TestValidatePolicy_ValidDefaults(t *testing.T) {
	if msg := ValidatePolicy(DefaultPolicy()); msg != "" {
		t.Errorf("default policy should be valid, got: %s", msg)
	}
}

func TestValidatePolicy_AcceptsRestartRestore(t *testing.T) {
	p := DefaultPolicy()
	p.RestartRestore = true
	if msg := ValidatePolicy(p); msg != "" {
		t.Errorf("expected restartRestore=true to be accepted, got: %s", msg)
	}
}

func TestValidatePolicy_RejectsUnsupportedOnDisconnect(t *testing.T) {
	p := DefaultPolicy()
	p.OnDisconnect = "terminate"
	if msg := ValidatePolicy(p); msg == "" {
		t.Error("expected error for unsupported onDisconnect")
	}
}

func TestValidatePolicy_RejectsUnsupportedOnCoreShutdown(t *testing.T) {
	p := DefaultPolicy()
	p.OnCoreShutdown = "leave_running"
	if msg := ValidatePolicy(p); msg == "" {
		t.Error("expected error for unsupported onCoreShutdown")
	}
}

func TestValidatePolicy_AcceptsOnCoreShutdownKeepRunning(t *testing.T) {
	p := DefaultPolicy()
	p.OnCoreShutdown = OnCoreShutdownKeepRunning
	if msg := ValidatePolicy(p); msg != "" {
		t.Errorf("expected onCoreShutdown=%q to be accepted, got: %s", OnCoreShutdownKeepRunning, msg)
	}
}

func TestStore_Persistence_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/runs.json"

	s1, err := NewStoreWithPath(path)
	if err != nil {
		t.Fatalf("NewStoreWithPath: %v", err)
	}

	r := s1.Create(&Run{
		NodeID:   "n1",
		Kind:     KindTerminal,
		Label:    "persist-test",
		PluginID: "terminal",
		Policy:   DefaultPolicy(),
	})

	// Read back from a fresh store
	s2, err := LoadFromDisk(path)
	if err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}

	got := s2.Get(r.RunID)
	if got == nil {
		t.Fatal("run not found after reload")
	}
	if got.Label != "persist-test" {
		t.Errorf("Label = %q, want %q", got.Label, "persist-test")
	}
	if got.State != StateRunning {
		t.Errorf("State = %q, want %q", got.State, StateRunning)
	}
}

func TestStore_Persistence_CounterRecovery(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/runs.json"

	s1, _ := NewStoreWithPath(path)
	s1.Create(&Run{NodeID: "n1", Kind: KindTerminal})
	s1.Create(&Run{NodeID: "n1", Kind: KindTerminal})
	s1.Create(&Run{NodeID: "n1", Kind: KindTerminal})

	s2, _ := LoadFromDisk(path)
	// Counter should be >= 3 so next run ID is new
	r := s2.Create(&Run{NodeID: "n1", Kind: KindTerminal})
	if r.RunID == "" {
		t.Fatal("new run ID should not be empty")
	}
	// New store should have 4 runs
	if s2.Count() != 4 {
		t.Errorf("Count = %d, want 4", s2.Count())
	}
}

func TestStore_Persistence_DeletePersisted(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/runs.json"

	s1, _ := NewStoreWithPath(path)
	r1 := s1.Create(&Run{NodeID: "n1", Kind: KindTerminal})
	r2 := s1.Create(&Run{NodeID: "n1", Kind: KindTerminal})

	s1.Delete(r1.RunID)

	s2, _ := LoadFromDisk(path)
	if s2.Get(r1.RunID) != nil {
		t.Error("deleted run should not be in reloaded store")
	}
	if s2.Get(r2.RunID) == nil {
		t.Error("non-deleted run should still be in reloaded store")
	}
	if s2.Count() != 1 {
		t.Errorf("Count = %d, want 1", s2.Count())
	}
}

func TestStore_Persistence_StateUpdatePersisted(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/runs.json"

	s1, _ := NewStoreWithPath(path)
	r := s1.Create(&Run{NodeID: "n1", Kind: KindTerminal})
	s1.UpdateState(r.RunID, StateOrphaned)

	s2, _ := LoadFromDisk(path)
	got := s2.Get(r.RunID)
	if got.State != StateOrphaned {
		t.Errorf("State = %q, want %q", got.State, StateOrphaned)
	}
}

func TestStore_Persistence_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/nonexistent.json"

	s, err := NewStoreWithPath(path)
	if err != nil {
		t.Fatalf("NewStoreWithPath with nonexistent file: %v", err)
	}
	if s.Count() != 0 {
		t.Errorf("Count = %d, want 0", s.Count())
	}

	// Creating a run should work and persist
	s.Create(&Run{NodeID: "n1", Kind: KindTerminal})

	s2, _ := LoadFromDisk(path)
	if s2.Count() != 1 {
		t.Errorf("Count after reload = %d, want 1", s2.Count())
	}
}
