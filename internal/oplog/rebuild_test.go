package oplog

import (
	"encoding/json"
	"testing"

	"github.com/PENG1028/sessionbridge-core/internal/plan"
	"github.com/PENG1028/sessionbridge-core/internal/run"
	"github.com/PENG1028/sessionbridge-core/internal/session"
	"github.com/PENG1028/sessionbridge-core/pkg/types"
)

func TestRebuild_SessionCreate(t *testing.T) {
	sessStore := session.NewStore()
	runStore := run.NewStore()

	snapshot := map[string]interface{}{
		"id":        "sess_1",
		"pluginId":  "test-plugin",
		"command":   "bash",
		"cwd":      "/tmp",
		"state":    session.StateRunning,
		"createdAt": int64(1000),
		"updatedAt": int64(2000),
	}

	op := &types.Operation{
		Capability: "session.create",
		Class:      types.OpClassState,
		StateAfter: &types.StateSnapshot{
			Type:     "session",
			StateID:  "sess_1",
			Snapshot: snapshot,
		},
	}

	RebuildFromOp(op, sessStore, runStore, nil)

	sess := sessStore.Get("sess_1")
	if sess == nil {
		t.Fatal("session not found after rebuild")
	}
	if string(sess.PluginID) != "test-plugin" {
		t.Errorf("pluginId = %q, want %q", sess.PluginID, "test-plugin")
	}
	if sess.State != session.StateRunning {
		t.Errorf("state = %q, want %q", sess.State, session.StateRunning)
	}
	if sess.Command != "bash" {
		t.Errorf("command = %q, want %q", sess.Command, "bash")
	}
}

func TestRebuild_SessionDestroy(t *testing.T) {
	sessStore := session.NewStore()
	runStore := run.NewStore()

	// Create a session first
	sessStore.Rebuild("sess_1", "plugin", "cmd", "/tmp", session.StateCreated, 1000, 1000)

	// Replay destroy
	op := &types.Operation{
		Capability: "session.destroy",
		StateAfter: &types.StateSnapshot{
			Type:    "session",
			StateID: "sess_1",
		},
	}
	RebuildFromOp(op, sessStore, runStore, nil)

	if sess := sessStore.Get("sess_1"); sess != nil {
		t.Error("session should be destroyed")
	}
}

func TestRebuild_RunCreateAndStop(t *testing.T) {
	runStore := run.NewStore()

	// Run create
	createSnapshot := map[string]interface{}{
		"runId":     "run_1",
		"kind":      "shell",
		"state":     run.StateRunning,
		"sessionId": "sess_1",
		"createdAt": int64(1000),
		"updatedAt": int64(1000),
	}

	RebuildFromOp(&types.Operation{
		Capability: "run.create",
		Class:      types.OpClassState,
		StateAfter: &types.StateSnapshot{
			Type:     "run",
			StateID:  "run_1",
			Snapshot: createSnapshot,
		},
	}, session.NewStore(), runStore, nil)

	r := runStore.Get("run_1")
	if r == nil {
		t.Fatal("run not found after rebuild")
	}
	if r.Kind != "shell" {
		t.Errorf("kind = %q, want %q", r.Kind, "shell")
	}
	if r.State != run.StateRunning {
		t.Errorf("state = %q, want %q", r.State, run.StateRunning)
	}
	if r.RunID != "run_1" {
		t.Errorf("runId = %q, want %q", r.RunID, "run_1")
	}

	// Run stop
	stopSnapshot := map[string]interface{}{
		"runId":     "run_1",
		"kind":      "shell",
		"state":     run.StateExited,
		"sessionId": "sess_1",
		"createdAt": int64(1000),
		"updatedAt": int64(2000),
	}

	RebuildFromOp(&types.Operation{
		Capability: "run.stop",
		Class:      types.OpClassState,
		StateAfter: &types.StateSnapshot{
			Type:     "run",
			StateID:  "run_1",
			Snapshot: stopSnapshot,
		},
	}, session.NewStore(), runStore, nil)

	r = runStore.Get("run_1")
	if r == nil {
		t.Fatal("run not found after stop rebuild")
	}
	if r.State != run.StateExited {
		t.Errorf("state = %q after stop, want %q", r.State, run.StateExited)
	}
}

func TestRebuild_FullLifecycle(t *testing.T) {
	// Simulate: session.create → run.create → run.stop → session.destroy
	sessStore := session.NewStore()
	runStore := run.NewStore()

	ops := []*types.Operation{
		{
			Capability: "session.create",
			StateAfter: &types.StateSnapshot{
				Type:    "session",
				StateID: "sess_1",
				Snapshot: map[string]interface{}{
					"id": "sess_1", "pluginId": "test", "command": "bash",
					"state": session.StateCreated, "createdAt": int64(100),
				},
			},
		},
		{
			Capability: "run.create",
			StateAfter: &types.StateSnapshot{
				Type:    "run",
				StateID: "run_1",
				Snapshot: map[string]interface{}{
					"runId": "run_1", "kind": "shell", "state": run.StateRunning,
					"sessionId": "sess_1", "createdAt": int64(200),
				},
			},
		},
		{
			Capability: "run.stop",
			StateAfter: &types.StateSnapshot{
				Type:    "run",
				StateID: "run_1",
				Snapshot: map[string]interface{}{
					"runId": "run_1", "kind": "shell", "state": run.StateExited,
					"sessionId": "sess_1", "createdAt": int64(200), "updatedAt": int64(300),
				},
			},
		},
		{
			Capability: "session.destroy",
			StateAfter: &types.StateSnapshot{
				Type:    "session",
				StateID: "sess_1",
			},
		},
	}

	for _, op := range ops {
		RebuildFromOp(op, sessStore, runStore, nil)
	}

	// Verify final state
	if sess := sessStore.Get("sess_1"); sess != nil {
		t.Error("session should be destroyed")
	}
	r := runStore.Get("run_1")
	if r == nil {
		t.Fatal("run should exist after lifecycle rebuild")
	}
	if r.State != run.StateExited {
		t.Errorf("run state = %q, want %q", r.State, run.StateExited)
	}
}

func TestRebuild_EmptyOpLog(t *testing.T) {
	sessStore := session.NewStore()
	runStore := run.NewStore()
	planStore := plan.NewPlanStore()

	// No operations → stores remain empty
	if count := sessStore.Count(); count != 0 {
		t.Errorf("expected 0 sessions, got %d", count)
	}
	if count := runStore.Count(); count != 0 {
		t.Errorf("expected 0 runs, got %d", count)
	}
	if ps := planStore.List(); len(ps) != 0 {
		t.Errorf("expected 0 plans, got %d", len(ps))
	}
}

func TestRebuild_FromRealOpLog(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, WithChunkSize(10))
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Write real operations with proper JSON-serializable StateAfter
	s.Append(&types.Operation{
		Capability: "session.create",
		Class:      types.OpClassState,
		StateAfter: &types.StateSnapshot{
			Type:    "session",
			StateID: "sess_1",
			Snapshot: map[string]interface{}{
				"id": "sess_1", "pluginId": "rebuild-test", "command": "zsh",
			},
		},
		Actor: types.Actor{Type: "web", ID: "tester"},
	})
	s.Append(&types.Operation{
		Capability: "run.create",
		Class:      types.OpClassState,
		StateAfter: &types.StateSnapshot{
			Type:    "run",
			StateID: "run_1",
			Snapshot: map[string]interface{}{
				"runId": "run_1", "kind": "shell", "state": run.StateRunning,
			},
		},
		Actor: types.Actor{Type: "web", ID: "tester"},
	})
	s.Close()

	// Re-open and rebuild
	s2 := NewStore(dir, WithChunkSize(10))
	if err := s2.Load(); err != nil {
		t.Fatalf("Load s2: %v", err)
	}

	sessStore := session.NewStore()
	runStore := run.NewStore()
	for _, op := range s2.Replay("") {
		RebuildFromOp(op, sessStore, runStore, nil)
	s2.Close()
	}

	if sess := sessStore.Get("sess_1"); sess == nil {
		t.Error("session not rebuilt from real OpLog")
	} else if string(sess.PluginID) != "rebuild-test" {
		t.Errorf("pluginId = %q", sess.PluginID)
	}

	if r := runStore.Get("run_1"); r == nil {
		t.Error("run not rebuilt from real OpLog")
	} else if r.Kind != "shell" {
		t.Errorf("kind = %q", r.Kind)
	}
}

func TestRebuild_StateAfterJSONRoundTrip(t *testing.T) {
	// Verify that StateAfter.Snapshot survives JSON marshal/unmarshal
	// because in the real OpLog, everything goes through JSON
	raw, _ := json.Marshal(map[string]interface{}{
		"id": "sess_json", "pluginId": "test", "command": "python",
	})
	var snapshot map[string]interface{}
	json.Unmarshal(raw, &snapshot)

	op := &types.Operation{
		Capability: "session.create",
		StateAfter: &types.StateSnapshot{
			Type:    "session",
			StateID: "sess_json",
			Snapshot: snapshot,
		},
	}

	// Verify it survives JSON round-trip
	opJSON, _ := json.Marshal(op)
	var restored types.Operation
	if err := json.Unmarshal(opJSON, &restored); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if restored.StateAfter.StateID != "sess_json" {
		t.Errorf("StateID = %q", restored.StateAfter.StateID)
	}
}
