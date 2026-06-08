package executor

import (
	"testing"
	"time"

	"github.com/user/sessionnode/go-core/internal/testutil"
)

// TestRunAndProcessList_CrossReference verifies that:
//  1. A process created via run.create has its RunID set and process.list
//     returns run metadata (runLabel, runKind, runState).
//  2. A process created via process.spawn (without run.create) has empty
//     runId in process.list.
//  3. The runId in process.list can be used to look up the same run via run.list.
//
// This is the e2e contract for the unified data-source model: the two APIs
// draw from the same reconciled state and never disagree.
func TestRunAndProcessList_CrossReference(t *testing.T) {
	deps := testDeps(t)
	defer deps.Processes.Cleanup()

	r := New(deps)

	sleepBin := testutil.SleepBinary(t)
	runResult := execOK(t, r, "run.create", map[string]interface{}{
		"command": sleepBin,
		"args":    []string{"60"},
		"kind":    "shell",
		"label":   "e2e-test-shell",
		"pty":     false,
	})
	runID, ok := runResult["runId"].(string)
	if !ok || runID == "" {
		t.Fatalf("run.create: expected non-empty runId, got %v", runResult["runId"])
	}
	sessionID, ok := runResult["sessionId"].(string)
	if !ok || sessionID == "" {
		t.Fatalf("run.create: expected non-empty sessionId, got %v", runResult["sessionId"])
	}
	t.Logf("Created run: runId=%s sessionId=%s", runID, sessionID)

	time.Sleep(500 * time.Millisecond)

	// ── process.list must include run metadata ────────────────
	procList := execOK(t, r, "process.list", nil)
	procs := procList["processes"].([]interface{})

	var matchedProc map[string]interface{}
	for _, p := range procs {
		entry := p.(map[string]interface{})
		if entry["sessionId"] == sessionID {
			matchedProc = entry
			break
		}
	}
	if matchedProc == nil {
		t.Fatalf("process.list: no process found with sessionId=%s", sessionID)
	}

	if got := matchedProc["runId"].(string); got != runID {
		t.Errorf("process.list runId = %q, want %q", got, runID)
	}
	if got := matchedProc["runLabel"].(string); got != "e2e-test-shell" {
		t.Errorf("process.list runLabel = %q, want %q", got, "e2e-test-shell")
	}
	if got := matchedProc["runKind"].(string); got != "shell" {
		t.Errorf("process.list runKind = %q, want %q", got, "shell")
	}
	if got := matchedProc["runState"].(string); got != "running" {
		t.Errorf("process.list runState = %q, want %q", got, "running")
	}
	t.Logf("process.list cross-reference OK: runId=%s label=%s kind=%s state=%s",
		matchedProc["runId"], matchedProc["runLabel"], matchedProc["runKind"], matchedProc["runState"])

	// ── run.list must agree (same sessionId) ─────────────────
	runList := execOK(t, r, "run.list", nil)
	runs := runList["runs"].([]interface{})
	var matchedRun map[string]interface{}
	for _, rn := range runs {
		entry := rn.(map[string]interface{})
		if entry["runId"] == runID {
			matchedRun = entry
			break
		}
	}
	if matchedRun == nil {
		t.Fatalf("run.list: runId=%s not found", runID)
	}
	runListSessionID := matchedRun["sessionId"].(string)
	if runListSessionID != sessionID {
		t.Errorf("run.list sessionId = %q, process.list sessionId = %q — mismatch", runListSessionID, sessionID)
	}
	t.Logf("run.list agrees: runId=%s sessionId=%s label=%s",
		runID, runListSessionID, matchedRun["label"])

	// ── process.spawn (no run) must have empty runId ─────────
	spawnResult := execOK(t, r, "process.spawn", map[string]interface{}{
		"command": sleepBin,
		"args":    []string{"30"},
	})
	spawnSessionID := spawnResult["sessionId"].(string)

	procList2 := execOK(t, r, "process.list", nil)
	procs2 := procList2["processes"].([]interface{})
	var spawnedProc map[string]interface{}
	for _, p := range procs2 {
		entry := p.(map[string]interface{})
		if entry["sessionId"] == spawnSessionID {
			spawnedProc = entry
			break
		}
	}
	if spawnedProc == nil {
		t.Fatalf("process.list: spawned process with sessionId=%s not found", spawnSessionID)
	}
	if runIDVal, exists := spawnedProc["runId"]; exists && runIDVal != "" {
		t.Errorf("spawned process should have empty runId, got %q", runIDVal)
	}
	if _, exists := spawnedProc["runLabel"]; exists {
		t.Errorf("spawned process should not have runLabel field")
	}
	t.Logf("process.spawn correctly has empty runId: sessionId=%s", spawnSessionID)
}
