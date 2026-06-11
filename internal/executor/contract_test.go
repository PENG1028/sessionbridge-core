package executor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"github.com/PENG1028/sessionbridge-core/internal/content"
	"github.com/PENG1028/sessionbridge-core/internal/oplog"
	"github.com/PENG1028/sessionbridge-core/pkg/types"
	"testing"

	"github.com/PENG1028/sessionbridge-core/internal/testutil"
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


// testDepsWithOpLog creates Deps with a real OpLog and ContentStore.
func testDepsWithOpLog(t *testing.T) *Deps {
	t.Helper()
	d := testDeps(t)
	tmpDir := t.TempDir()
	opLog := oplog.NewStore(filepath.Join(tmpDir, "oplog"), oplog.WithChunkSize(10))
	if err := opLog.Load(); err != nil {
		t.Fatalf("oplog load: %v", err)
	}
	t.Cleanup(func() { opLog.Close() })
	cs := content.NewStore(filepath.Join(tmpDir, "trash"))
	if err := cs.Load(); err != nil {
		t.Fatalf("content load: %v", err)
	}
	d.OpLog = opLog
	d.RollbackEngine = oplog.NewRollbackEngine(opLog, cs)
	return d
}

func TestContract_OperationsListShape(t *testing.T) {
	d := testDepsWithOpLog(t)
	r := New(d)

	// Pre-populate OpLog with test operations
	d.OpLog.Append(&types.Operation{Capability: "session.create", Class: types.OpClassState, Actor: types.Actor{Type: "web", ID: "tester"}})
	d.OpLog.Append(&types.Operation{Capability: "config.set", Class: types.OpClassContent, Actor: types.Actor{Type: "admin", ID: "u1"}})

	result, err := r.Execute(req("operations.list", nil))
	if err != nil {
		t.Fatalf("operations.list: %v", err)
	}
	m := normalize(result).(map[string]interface{})

	opsRaw, ok := m["operations"]
	if !ok {
		t.Fatal("operations.list: missing top-level key 'operations'")
	}
	ops, ok := opsRaw.([]interface{})
	if !ok {
		t.Fatal("operations.list: 'operations' must be an array")
	}
	if len(ops) == 0 {
		t.Fatal("operations.list: expected at least 1 operation")
	}

	entry := ops[0].(map[string]interface{})
	requiredFields := []string{"opId", "class", "capability", "actor", "timestamp"}
	for _, field := range requiredFields {
		if _, exists := entry[field]; !exists {
			t.Errorf("operations.list entry: missing required field %q", field)
		}
	}
	if _, err := json.Marshal(entry); err != nil {
		t.Errorf("operations.list entry: invalid JSON: %v", err)
	}
	t.Logf("OK operations.list: %d ops, fields: %v", len(ops), requiredFields)
}

func TestContract_OperationsGetShape(t *testing.T) {
	d := testDepsWithOpLog(t)
	r := New(d)

	d.OpLog.Append(&types.Operation{Capability: "run.create", Class: types.OpClassState, Actor: types.Actor{Type: "web", ID: "tester"}})

	listResult, _ := r.Execute(req("operations.list", nil))
	lm := normalize(listResult).(map[string]interface{})
	ops := lm["operations"].([]interface{})
	if len(ops) == 0 {
		t.Fatal("no operations to test get")
	}
	opID := ops[0].(map[string]interface{})["opId"].(string)

	result, err := r.Execute(req("operations.get", map[string]string{"opId": opID}))
	if err != nil {
		t.Fatalf("operations.get: %v", err)
	}
	m := normalize(result).(map[string]interface{})
	op, ok := m["operation"]
	if !ok {
		t.Fatal("operations.get: missing 'operation' key")
	}
	entry := op.(map[string]interface{})
	if entry["opId"] != opID {
		t.Errorf("operations.get: opId = %v, want %s", entry["opId"], opID)
	}
	if _, err := json.Marshal(entry); err != nil {
		t.Errorf("operations.get entry: invalid JSON: %v", err)
	}
	t.Logf("OK operations.get: opId=%s", opID)
}

func TestContract_OperationsGetNotFound(t *testing.T) {
	d := testDepsWithOpLog(t)
	r := New(d)
	if _, err := r.Execute(req("operations.get", map[string]string{"opId": "op_nonexistent"})); err == nil {
		t.Fatal("expected error for nonexistent op")
	}
}

func TestContract_OperationsDryRunShape(t *testing.T) {
	d := testDepsWithOpLog(t)
	r := New(d)

	tmpFile := filepath.Join(t.TempDir(), "dryrun_test.txt")
	os.WriteFile(tmpFile, []byte("original"), 0644)
	data, _ := os.ReadFile(tmpFile)
	ref, _ := d.RollbackEngine.Content.Store(data)

	op := &types.Operation{
		Capability: "fs.write", Class: types.OpClassContent,
		ContentBefore: &types.ContentRef{Path: tmpFile, Hash: ref.Hash, Size: ref.Size, StoredAt: ref.StoredAt},
		Actor: types.Actor{Type: "test", ID: "dry-runner"},
	}
	opID, _ := d.OpLog.Append(op)

	result, err := r.Execute(req("operations.dryRun", map[string]string{"opId": string(opID)}))
	if err != nil {
		t.Fatalf("operations.dryRun: %v", err)
	}
	m := normalize(result).(map[string]interface{})
	preview, ok := m["preview"]
	if !ok {
		t.Fatal("operations.dryRun: missing 'preview' key")
	}
	p := preview.(map[string]interface{})
	if p["opId"] != string(opID) {
		t.Errorf("preview.opId = %v, want %s", p["opId"], string(opID))
	}
	if _, ok := p["willRestore"]; !ok {
		t.Error("preview: missing 'willRestore' for content op")
	}
	if _, err := json.Marshal(p); err != nil {
		t.Errorf("dryRun preview: invalid JSON: %v", err)
	}
	t.Logf("OK operations.dryRun: opId=%s", opID)
}

func TestContract_OperationsListFilter(t *testing.T) {
	d := testDepsWithOpLog(t)
	r := New(d)
	d.OpLog.Append(&types.Operation{Capability: "session.create", Class: types.OpClassState, Actor: types.Actor{Type: "web", ID: "tester"}})

	result, err := r.Execute(req("operations.list", map[string]string{"class": "C"}))
	if err != nil {
		t.Fatalf("operations.list class=C: %v", err)
	}
	m := normalize(result).(map[string]interface{})
	ops := m["operations"].([]interface{})
	if len(ops) == 0 {
		t.Error("operations.list class=C returned 0 (expected >=1)")
	} else {
		t.Logf("OK operations.list filter class=C: %d results", len(ops))
	}
}


func TestContract_RollbackE2E(t *testing.T) {
	d := testDepsWithOpLog(t)
	r := New(d)

	// Create a file
	tmpFile := filepath.Join(t.TempDir(), "rollback_e2e.txt")
	origData := []byte("ORIGINAL CONTENT")
	if err := os.WriteFile(tmpFile, origData, 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Store original content
	data, _ := os.ReadFile(tmpFile)
	ref, err := d.RollbackEngine.Content.Store(data)
	if err != nil {
		t.Fatalf("content store: %v", err)
	}

	// Record an fs.write operation with content backup
	op := &types.Operation{
		Capability: "fs.write",
		Class:      types.OpClassContent,
		ContentBefore: &types.ContentRef{
			Path: tmpFile, Hash: ref.Hash, Size: ref.Size, StoredAt: ref.StoredAt,
		},
		Actor: types.Actor{Type: "test", ID: "e2e-test"},
	}
	opID, _ := d.OpLog.Append(op)

	// Overwrite the file
	if err := os.WriteFile(tmpFile, []byte("NEW CONTENT"), 0644); err != nil {
		t.Fatalf("overwrite file: %v", err)
	}

	// Verify file has new content
	current, _ := os.ReadFile(tmpFile)
	if string(current) != "NEW CONTENT" {
		t.Fatalf("expected new content before rollback, got %q", current)
	}

	// Execute rollback through operations.rollback API
	rollbackResult, err := r.Execute(req("operations.rollback", map[string]string{"opId": string(opID)}))
	if err != nil {
		t.Fatalf("operations.rollback: %v", err)
	}
	m := normalize(rollbackResult).(map[string]interface{})
	if m["rolledBack"] != true {
		t.Error("rollback: expected rolledBack=true")
	}
	if m["opId"] != string(opID) {
		t.Errorf("rollback: opId = %v, want %s", m["opId"], string(opID))
	}

	// Verify file was restored
	restored, _ := os.ReadFile(tmpFile)
	if string(restored) != "ORIGINAL CONTENT" {
		t.Errorf("rollback E2E: expected restored content %q, got %q", "ORIGINAL CONTENT", string(restored))
	} else {
		t.Logf("M-[M-@M-^U Rollback E2E: file restored correctly")
	}

	// Verify rollback event recorded
	listResult, _ := r.Execute(req("operations.list", nil))
	lm := normalize(listResult).(map[string]interface{})
	ops := lm["operations"].([]interface{})
	// Should have 2 ops: the fs.write and the _rollback event
	if len(ops) < 2 {
		t.Errorf("expected at least 2 ops (write + rollback event), got %d", len(ops))
	} else {
		t.Logf("M-[M-@M-^U Rollback event recorded: %d total ops", len(ops))
	}
}
