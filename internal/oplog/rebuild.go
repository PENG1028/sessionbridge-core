// Package oplog implements append-only Operation Log and restart recovery.
package oplog

import (
	"github.com/PENG1028/sessionbridge-core/internal/plan"
	"github.com/PENG1028/sessionbridge-core/internal/run"
	"github.com/PENG1028/sessionbridge-core/internal/session"
	"github.com/PENG1028/sessionbridge-core/pkg/types"
)

// RebuildSessionFromOp reconstructs a session from a session.create Operation.
func RebuildSessionFromOp(op *types.Operation, sStore *session.Store) {
	if op.StateAfter == nil || op.StateAfter.Type != "session" {
		return
	}

	// Extract session fields from StateAfter snapshot
	snapshot, ok := op.StateAfter.Snapshot.(map[string]interface{})
	if !ok {
		return
	}

	sid := safeString(snapshot, "id")
	if sid == "" {
		// Fall back to StateID
		sid = op.StateAfter.StateID
	}
	if sid == "" {
		return
	}

	pluginID := safeString(snapshot, "pluginId")
	command := safeString(snapshot, "command")
	cwd := safeString(snapshot, "cwd")
	state := safeString(snapshot, "state")
	if state == "" {
		state = session.StateCreated
	}
	createdAt := safeInt64(snapshot, "createdAt", op.Timestamp)
	updatedAt := safeInt64(snapshot, "updatedAt", op.Timestamp)

	sStore.Rebuild(
		types.SessionID(sid),
		types.PluginID(pluginID),
		command, cwd,
		state, createdAt, updatedAt,
	)
}

// RebuildRunFromOp reconstructs a run from a run.create Operation.
func RebuildRunFromOp(op *types.Operation, rStore *run.Store) {
	if op.StateAfter == nil || op.StateAfter.Type != "run" {
		return
	}

	snapshot, ok := op.StateAfter.Snapshot.(map[string]interface{})
	if !ok {
		return
	}

	runID := safeString(snapshot, "runId")
	if runID == "" {
		runID = op.StateAfter.StateID
	}
	if runID == "" {
		return
	}

	r := &run.Run{
		RunID:     runID,
		Kind:      safeString(snapshot, "kind"),
		State:     safeString(snapshot, "state"),
		SessionID: types.SessionID(safeString(snapshot, "sessionId")),
		ProcessID: types.SessionID(safeString(snapshot, "processId")),
		CreatedAt: safeInt64(snapshot, "createdAt", op.Timestamp),
		UpdatedAt: safeInt64(snapshot, "updatedAt", op.Timestamp),
	}
	if r.State == "" {
		r.State = run.StateRunning
	}
	rStore.Rebuild(r)

	// Handle state updates from other ops
	switch op.Capability {
	case "run.stop":
		rStore.UpdateState(runID, safeString(snapshot, "state"))
	case "run.updatePolicy":
		if p, ok := snapshot["policy"].(map[string]interface{}); ok {
			policy := run.Policy{
				OnDisconnect: safeString(p, "onDisconnect"),
				OnCoreShutdown: safeString(p, "onCoreShutdown"),
				RestartRestore: safeBool(p, "restartRestore"),
			}
			rStore.UpdatePolicy(runID, policy)
		}
	case "run.attach":
		rStore.SaveProcessRef(runID, types.SessionID(safeString(snapshot, "processId")), safeString(snapshot, "state"))
	}
}

// RebuildPlanFromOp reconstructs a plan from a plan.create Operation.
func RebuildPlanFromOp(op *types.Operation, pStore *plan.PlanStore) {
	if op.StateAfter == nil || op.StateAfter.Type != "plan" {
		return
	}

	snapshot, ok := op.StateAfter.Snapshot.(map[string]interface{})
	if !ok {
		return
	}

	planID := safeString(snapshot, "id")
	if planID == "" {
		planID = op.StateAfter.StateID
	}
	if planID == "" {
		return
	}

	p := plan.NewPlan(
		planID,
		safeString(snapshot, "capability"),
		safeString(snapshot, "summary"),
		safeString(snapshot, "description"),
		nil, /* details — not persisted */
		safeString(snapshot, "createdBy"),
		0, /* use default TTL */
	)
	// Override state and timestamps from snapshot
	state := safeString(snapshot, "state")
	if state != "" && state != plan.StatePending {
		// Direct state set (bypasses transition validation for recovery)
		p.State = state
	}
	p.CreatedAt = safeInt64(snapshot, "createdAt", op.Timestamp)
	p.UpdatedAt = safeInt64(snapshot, "updatedAt", op.Timestamp)

	pStore.Create(p)
}

// RebuildFromOp dispatches an Operation to the appropriate store rebuild function.
func RebuildFromOp(op *types.Operation, sessStore *session.Store, runStore *run.Store, planStore *plan.PlanStore) {
	switch op.Capability {
	case "session.create":
		RebuildSessionFromOp(op, sessStore)
	case "session.destroy":
		if op.StateAfter != nil {
			sessStore.Destroy(types.SessionID(op.StateAfter.StateID))
		}
	case "run.create":
		RebuildRunFromOp(op, runStore)
	case "run.stop", "run.updatePolicy", "run.attach":
		RebuildRunFromOp(op, runStore)
	case "plan.create":
		RebuildPlanFromOp(op, planStore)
	}
}

// --- snapshot field helpers ---

func safeString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func safeInt64(m map[string]interface{}, key string, fallback int64) int64 {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int64(n)
		case int64:
			return n
		case int:
			return int64(n)
		}
	}
	return fallback
}

func safeBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}
