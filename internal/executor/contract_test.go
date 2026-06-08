package executor

import (
	"encoding/json"
	"testing"

	"github.com/user/sessionnode/go-core/internal/testutil"
)

// ─── Contract Tests ─────────────────────────────────────────────────
//
// These tests document and enforce the EXACT JSON shape of run.list
// and process.list responses. They serve as the API contract between
// Go Core and its clients (App UI, plugins, mesh peers).
//
// The contract guarantees:
//   - runId in process.list always matches a run.list entry's runId
//     when the process was created via run.create.
//   - Processes created via process.spawn (no run.create) have empty
//     runId and no run* fields.
//   - The two APIs never produce contradictory data for the same
//     underlying process.
//
// If you change the response shape in run_cmds.go or process_cmds.go,
// you MUST update these tests and the TypeScript types in core-types.ts.

// TestContract_RunListShape validates the run.list response shape.
func TestContract_RunListShape(t *testing.T) {
	deps := testDeps(t)
	defer deps.Processes.Cleanup()
	r := New(deps)

	sleepBin := testutil.SleepBinary(t)
	execOK(t, r, "run.create", map[string]interface{}{
		"command": sleepBin,
		"args":    []string{"60"},
		"kind":    "shell",
		"label":   "contract-test",
		"pty":     false,
	})

	result := execOK(t, r, "run.list", nil)

	// ── Top-level contract ──
	// run.list MUST return {"runs": [...], ...}
	runsRaw, ok := result["runs"]
	if !ok {
		t.Fatal("run.list: missing required top-level key 'runs'")
	}
	runs, ok := runsRaw.([]interface{})
	if !ok {
		t.Fatal("run.list: 'runs' must be an array")
	}
	if len(runs) == 0 {
		t.Fatal("run.list: expected at least one run")
	}

	// ── Per-entry field contract ──
	entry := runs[0].(map[string]interface{})
	requiredFields := []string{
		"runId",     // string, stable identifier
		"sessionId", // string, links to process.sessionId
		"processId", // string, links to process.processId
		"kind",      // string, e.g. "shell", "terminal"
		"state",     // string, e.g. "running", "exited"
		"createdAt", // number, unix millis
		"updatedAt", // number, unix millis
	}
	for _, field := range requiredFields {
		if _, exists := entry[field]; !exists {
			t.Errorf("run.list entry: missing required field %q", field)
		}
	}

	// run.list MUST NOT include run* fields (those are process.list's job)
	runOnlyFields := []string{"runId", "runLabel", "runKind", "runState", "runPluginId"}
	for _, field := range runOnlyFields {
		if _, exists := entry[field]; exists && field != "runId" {
			t.Errorf("run.list entry: must NOT have field %q (it belongs to process.list)", field)
		}
	}

	// SessionID must be a non-empty string
	sid, _ := entry["sessionId"].(string)
	if sid == "" {
		t.Error("run.list: sessionId must be non-empty")
	}

	// Serialize to verify it produces valid JSON
	_, err := json.Marshal(entry)
	if err != nil {
		t.Errorf("run.list entry: invalid JSON: %v", err)
	}

	t.Logf("run.list contract OK: %d runs, fields: %v", len(runs), requiredFields)
}

// TestContract_ProcessListShape validates the process.list response
// shape, including the cross-reference fields (runId, run*).
func TestContract_ProcessListShape(t *testing.T) {
	deps := testDeps(t)
	defer deps.Processes.Cleanup()
	r := New(deps)

	sleepBin := testutil.SleepBinary(t)

	// ── 1. Create a run (to get a cross-referenced process) ──
	execOK(t, r, "run.create", map[string]interface{}{
		"command": sleepBin,
		"args":    []string{"60"},
		"kind":    "shell",
		"label":   "contract-test",
		"pty":     false,
	})

	// ── 2. Spawn a process WITHOUT run.create ──
	execOK(t, r, "process.spawn", map[string]interface{}{
		"command": sleepBin,
		"args":    []string{"60"},
	})

	result := execOK(t, r, "process.list", nil)

	// ── Top-level contract ──
	// process.list MUST return {"processes": [...], "total": N}
	procsRaw, ok := result["processes"]
	if !ok {
		t.Fatal("process.list: missing required top-level key 'processes'")
	}
	procs, ok := procsRaw.([]interface{})
	if !ok {
		t.Fatal("process.list: 'processes' must be an array")
	}
	if len(procs) == 0 {
		t.Fatal("process.list: expected at least one process")
	}

	totalRaw, ok := result["total"]
	if !ok {
		t.Fatal("process.list: missing required top-level key 'total'")
	}
	total, ok := totalRaw.(float64)
	if !ok || int(total) != len(procs) {
		t.Errorf("process.list: total (%v) must match processes length (%d)", totalRaw, len(procs))
	}

	// ── Per-entry field contract (applies to ALL processes) ──
	baseFields := []string{
		"sessionId",       // string, unique identifier
		"processId",       // string, same as sessionId
		"parentSessionId", // string, empty for root
		"rootSessionId",   // string, sessionId of root
		"pluginId",        // string, plugin that owns this process
		"kind",            // string, e.g. "shell", "task"
		"pid",             // number, OS PID
		"state",           // string, "running" | "exited"
		"exitCode",        // number, 0 while running
		"command",         // string, binary path
		"createdAt",       // number, unix millis
		"runId",           // string, empty for non-run processes
	}

	// Categorize processes: those with runId vs without
	var withRun, withoutRun map[string]interface{}
	for _, p := range procs {
		entry := p.(map[string]interface{})
		for _, field := range baseFields {
			if _, exists := entry[field]; !exists {
				t.Errorf("process.list entry %v: missing required field %q", entry["sessionId"], field)
			}
		}

		// Validate types for numeric fields
		pid, _ := entry["pid"].(float64)
		if pid <= 0 {
			t.Errorf("process.list entry %v: pid must be > 0, got %v", entry["sessionId"], pid)
		}

		if rid, _ := entry["runId"].(string); rid != "" && withRun == nil {
			withRun = entry
		} else if rid == "" && withoutRun == nil {
			withoutRun = entry
		}

		// Serialize to verify valid JSON
		_, err := json.Marshal(entry)
		if err != nil {
			t.Errorf("process.list entry %v: invalid JSON: %v", entry["sessionId"], err)
		}
	}

	if withRun == nil {
		t.Fatal("process.list: expected at least one process with non-empty runId (from run.create)")
	}
	if withoutRun == nil {
		t.Fatal("process.list: expected at least one process with empty runId (from process.spawn)")
	}

	// ── Contract for run-backed processes ──
	// Must have runLabel, runKind, runState, runPluginId
	runFields := []string{"runLabel", "runKind", "runState", "runPluginId"}
	for _, field := range runFields {
		if _, exists := withRun[field]; !exists {
			t.Errorf("process.list (run-backed): missing required field %q in %v", field, withRun["sessionId"])
		}
	}
	t.Logf("process.list run-backed entry OK: runId=%s fields=%v", withRun["runId"], runFields)

	// ── Contract for non-run processes ──
	// Must NOT have runLabel, runKind etc.
	for _, field := range runFields {
		if _, exists := withoutRun[field]; exists {
			t.Errorf("process.list (non-run): must NOT have field %q in %v", field, withoutRun["sessionId"])
		}
	}
	t.Logf("process.list non-run entry OK: sessionId=%s has empty runId", withoutRun["sessionId"])

	t.Logf("process.list contract OK: %d processes, %d base fields, %d run fields",
		len(procs), len(baseFields), len(runFields))
}

// TestContract_ProcessListRunIdMatchesRunList verifies that runId in
// process.list can be used to look up the matching entry in run.list.
// This is the core cross-referencing contract.
func TestContract_ProcessListRunIdMatchesRunList(t *testing.T) {
	deps := testDeps(t)
	defer deps.Processes.Cleanup()
	r := New(deps)

	sleepBin := testutil.SleepBinary(t)
	execOK(t, r, "run.create", map[string]interface{}{
		"command": sleepBin,
		"args":    []string{"60"},
		"kind":    "shell",
		"label":   "xref-test",
		"pty":     false,
	})

	procResult := execOK(t, r, "process.list", nil)
	procs := procResult["processes"].([]interface{})

	// Find the run-backed process
	var runProc map[string]interface{}
	for _, p := range procs {
		entry := p.(map[string]interface{})
		if rid, _ := entry["runId"].(string); rid != "" {
			runProc = entry
			break
		}
	}
	if runProc == nil {
		t.Fatal("no run-backed process found")
	}
	runID := runProc["runId"].(string)

	// Look up the same run in run.list
	runResult := execOK(t, r, "run.list", nil)
	runs := runResult["runs"].([]interface{})
	var matchedRun map[string]interface{}
	for _, rn := range runs {
		entry := rn.(map[string]interface{})
		if entry["runId"] == runID {
			matchedRun = entry
			break
		}
	}
	if matchedRun == nil {
		t.Fatalf("run.list: no entry with runId=%q that process.list claimed exists", runID)
	}

	// Verify cross-reference consistency
	procSID := runProc["sessionId"].(string)
	runSID := matchedRun["sessionId"].(string)
	if procSID != runSID {
		t.Errorf("sessionId mismatch: process.list says %q, run.list says %q", procSID, runSID)
	}

	procKind := runProc["runKind"].(string)
	runKind := matchedRun["kind"].(string)
	if procKind != runKind {
		t.Errorf("kind mismatch: process.list runKind=%q, run.list kind=%q", procKind, runKind)
	}

	t.Logf("Cross-reference contract OK: runId=%s sessionId=%s kind=%s", runID, procSID, procKind)
}
