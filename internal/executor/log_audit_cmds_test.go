package executor

import (
	"testing"

	"github.com/user/sessionnode/go-core/internal/logs"
)

func TestLogsTail_EmptyPayload(t *testing.T) {
	r := New(testDeps(t))
	result, err := r.Execute(req("logs.tail", nil))
	if err != nil {
		t.Fatalf("logs.tail with nil payload should not error: %v", err)
	}
	lines, ok := result.(map[string]interface{})["lines"]
	if !ok {
		t.Fatal("logs.tail result missing 'lines' key")
	}
	if lines == nil {
		t.Fatal("logs.tail 'lines' should not be nil")
	}
}

func TestLogsTail_EmptyResult(t *testing.T) {
	r := New(testDeps(t))
	result, err := r.Execute(req("logs.tail", map[string]interface{}{
		"source": "core",
		"lines":  100,
	}))
	if err != nil {
		t.Fatalf("logs.tail should not error: %v", err)
	}
	m := result.(map[string]interface{})
	if _, ok := m["lines"]; !ok {
		t.Fatal("missing 'lines' key")
	}
}

func TestLogsTail_UnknownSource(t *testing.T) {
	r := New(testDeps(t))
	result, err := r.Execute(req("logs.tail", map[string]interface{}{
		"source": "unknown_source_xyz",
		"lines":  50,
	}))
	if err != nil {
		t.Fatalf("logs.tail with unknown source should not error: %v", err)
	}
	m := result.(map[string]interface{})
	lines, ok := m["lines"]
	if !ok {
		t.Fatal("missing 'lines'")
	}
	// Should return empty array, not nil
	if lines == nil {
		t.Fatal("lines should be empty slice, not nil")
	}
}

func TestLogsTail_LimitClamp(t *testing.T) {
	r := New(testDeps(t))
	// limit > 1000 should be clamped
	result, err := r.Execute(req("logs.tail", map[string]interface{}{
		"source": "core",
		"lines":  5000,
	}))
	if err != nil {
		t.Fatalf("logs.tail with high limit should not error: %v", err)
	}
	m := result.(map[string]interface{})
	if _, ok := m["lines"]; !ok {
		t.Fatal("missing 'lines'")
	}
	// result is still a valid empty slice — clamping doesn't change empty output
}

func TestLogsQuery_EmptyPayload(t *testing.T) {
	r := New(testDeps(t))
	result, err := r.Execute(req("logs.query", nil))
	if err != nil {
		t.Fatalf("logs.query with nil payload should not error: %v", err)
	}
	entries, ok := result.(map[string]interface{})["entries"]
	if !ok {
		t.Fatal("logs.query result missing 'entries' key")
	}
	if entries == nil {
		t.Fatal("logs.query 'entries' should not be nil")
	}
}

func TestLogsQuery_WithPluginId(t *testing.T) {
	r := New(testDeps(t))
	result, err := r.Execute(req("logs.query", map[string]interface{}{
		"source":   "plugin",
		"pluginId": "terminal",
		"level":    "error",
		"limit":    100,
	}))
	if err != nil {
		t.Fatalf("logs.query should not error: %v", err)
	}
	m := result.(map[string]interface{})
	entries, ok := m["entries"]
	if !ok {
		t.Fatal("missing 'entries'")
	}
	if entries == nil {
		t.Fatal("entries should be empty slice, not nil")
	}
}

func TestLogsQuery_LimitClamp(t *testing.T) {
	r := New(testDeps(t))
	result, err := r.Execute(req("logs.query", map[string]interface{}{
		"limit": 2000,
	}))
	if err != nil {
		t.Fatalf("logs.query with high limit should not error: %v", err)
	}
	m := result.(map[string]interface{})
	if _, ok := m["entries"]; !ok {
		t.Fatal("missing 'entries'")
	}
}

func TestAuditList_EmptyPayload(t *testing.T) {
	r := New(testDeps(t))
	result, err := r.Execute(req("audit.list", nil))
	if err != nil {
		t.Fatalf("audit.list with nil payload should not error: %v", err)
	}
	entries, ok := result.(map[string]interface{})["entries"]
	if !ok {
		t.Fatal("audit.list result missing 'entries' key")
	}
	if entries == nil {
		t.Fatal("audit.list 'entries' should not be nil")
	}
}

func TestAuditList_WithFilters(t *testing.T) {
	r := New(testDeps(t))
	result, err := r.Execute(req("audit.list", map[string]interface{}{
		"timeRange": "24h",
		"type":      "capability.call",
		"actor":     "user",
		"target":    "plugin",
		"limit":     50,
	}))
	if err != nil {
		t.Fatalf("audit.list with filters should not error: %v", err)
	}
	m := result.(map[string]interface{})
	entries, ok := m["entries"]
	if !ok {
		t.Fatal("missing 'entries'")
	}
	if entries == nil {
		t.Fatal("entries should be empty slice, not nil")
	}
}

func TestAuditList_LimitClamp(t *testing.T) {
	r := New(testDeps(t))
	result, err := r.Execute(req("audit.list", map[string]interface{}{
		"limit": 9999,
	}))
	if err != nil {
		t.Fatalf("audit.list with excessive limit should not error: %v", err)
	}
	m := result.(map[string]interface{})
	if _, ok := m["entries"]; !ok {
		t.Fatal("missing 'entries'")
	}
}

func TestLogsAudit_RegistryHasHandlers(t *testing.T) {
	r := New(testDeps(t))
	for _, cap := range []string{"logs.tail", "logs.query", "audit.list"} {
		result, err := r.Execute(req(cap, nil))
		if err != nil {
			t.Errorf("capability %q should be registered: %v", cap, err)
		}
		if result == nil {
			t.Errorf("capability %q should return non-nil result", cap)
		}
	}
}

// ─── Real data tests (Phase 2: LogBuffer + AuditStore wired) ─────────────

func testDepsWithStores(t *testing.T) *Deps {
	t.Helper()
	d := testDeps(t)
	d.LogBuffer = logs.NewBuffer(100)
	d.AuditStore = logs.NewAuditStore()
	return d
}

func TestLogsTail_ReturnsStoredEntries(t *testing.T) {
	d := testDepsWithStores(t)
	d.LogBuffer.Add(logs.Entry{Timestamp: 1000, Level: "info", Source: "core", Message: "startup ok"})
	d.LogBuffer.Add(logs.Entry{Timestamp: 2000, Level: "error", Source: "core", Message: "capability failed"})

	r := New(d)
	result, err := r.Execute(req("logs.tail", map[string]interface{}{
		"source": "core",
		"lines":  50,
	}))
	if err != nil {
		t.Fatalf("logs.tail error: %v", err)
	}
	m := result.(map[string]interface{})

	lines, ok := m["lines"].([]logLineEntry)
	if !ok {
		t.Fatalf("lines is not []logLineEntry")
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if lines[0].Message != "startup ok" {
		t.Errorf("unexpected first line: %s", lines[0].Message)
	}
	if lines[1].Message != "capability failed" {
		t.Errorf("unexpected second line: %s", lines[1].Message)
	}

	// Verify entries key is also present
	entries, ok := m["entries"].([]logs.Entry)
	if !ok {
		t.Fatalf("entries is missing or wrong type")
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
}

func TestLogsTail_FiltersByLevel(t *testing.T) {
	d := testDepsWithStores(t)
	d.LogBuffer.Add(logs.Entry{Timestamp: 1000, Level: "info", Source: "core", Message: "ok"})
	d.LogBuffer.Add(logs.Entry{Timestamp: 2000, Level: "error", Source: "core", Message: "fail"})

	r := New(d)
	result, err := r.Execute(req("logs.tail", map[string]interface{}{
		"source": "core",
		"level":  "error",
		"lines":  50,
	}))
	if err != nil {
		t.Fatalf("logs.tail error: %v", err)
	}
	m := result.(map[string]interface{})
	lines := m["lines"].([]logLineEntry)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0].Message != "fail" {
		t.Errorf("expected 'fail', got %q", lines[0].Message)
	}
}

func TestLogsQuery_FiltersByPluginId(t *testing.T) {
	d := testDepsWithStores(t)
	d.LogBuffer.Add(logs.Entry{Timestamp: 1000, Level: "info", Source: "plugin", PluginID: "terminal", Message: "term-1"})
	d.LogBuffer.Add(logs.Entry{Timestamp: 2000, Level: "info", Source: "plugin", PluginID: "files", Message: "file-1"})

	r := New(d)
	result, err := r.Execute(req("logs.query", map[string]interface{}{
		"pluginId": "terminal",
		"limit":    50,
	}))
	if err != nil {
		t.Fatalf("logs.query error: %v", err)
	}
	m := result.(map[string]interface{})
	entries := m["entries"].([]logEntry)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Message != "term-1" {
		t.Errorf("expected 'term-1', got %q", entries[0].Message)
	}
}

func TestAuditList_ReturnsStoredRecords(t *testing.T) {
	d := testDepsWithStores(t)
	d.AuditStore.Record(logs.AuditRecord{
		AuditID:   "audit-1",
		Timestamp: 1000,
		EventType: "capability.call",
		Actor:     "web:alice",
		Target:    "core/system.info",
		Outcome:   "ok",
		Metadata:  map[string]interface{}{"requestId": "req-1"},
	})

	r := New(d)
	result, err := r.Execute(req("audit.list", map[string]interface{}{
		"limit": 50,
	}))
	if err != nil {
		t.Fatalf("audit.list error: %v", err)
	}
	m := result.(map[string]interface{})
	entries := m["entries"].([]auditEntry)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].AuditID != "audit-1" {
		t.Errorf("expected audit-1, got %s", entries[0].AuditID)
	}
	if entries[0].Actor != "web:alice" {
		t.Errorf("expected web:alice, got %s", entries[0].Actor)
	}
}

func TestAuditList_FiltersByType(t *testing.T) {
	d := testDepsWithStores(t)
	d.AuditStore.Record(logs.AuditRecord{EventType: "capability.call", Actor: "a", Target: "t", Outcome: "ok"})
	d.AuditStore.Record(logs.AuditRecord{EventType: "permission.grant", Actor: "a", Target: "t", Outcome: "ok"})

	r := New(d)
	result, err := r.Execute(req("audit.list", map[string]interface{}{
		"type":  "permission.grant",
		"limit": 50,
	}))
	if err != nil {
		t.Fatalf("audit.list error: %v", err)
	}
	m := result.(map[string]interface{})
	entries := m["entries"].([]auditEntry)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].EventType != "permission.grant" {
		t.Errorf("expected permission.grant, got %s", entries[0].EventType)
	}
}

func TestRegistryExecute_RecordsLogOnSuccess(t *testing.T) {
	d := testDepsWithStores(t)
	r := New(d)

	_, err := r.Execute(req("system.info", nil))
	if err != nil {
		t.Fatalf("system.info error: %v", err)
	}

	lines := d.LogBuffer.Tail("", "", 10)
	if len(lines) == 0 {
		t.Fatal("expected at least 1 log entry recorded for system.info")
	}
	found := false
	for _, e := range lines {
		if e.Message == "system.info ok" && e.Level == "info" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'system.info ok' (info) in log buffer, got: %+v", lines)
	}
}

func TestRegistryExecute_RecordsLogOnFailure(t *testing.T) {
	d := testDepsWithStores(t)
	r := New(d)

	_, _ = r.Execute(req("unknown.cap.xyz", nil))

	lines := d.LogBuffer.Tail("", "", 10)
	found := false
	for _, e := range lines {
		if e.Level == "error" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error-level log for unknown capability, got: %+v", lines)
	}
}

func TestRegistryExecute_SkipsObservabilityCaps(t *testing.T) {
	d := testDepsWithStores(t)
	r := New(d)

	// logs.tail should not be recorded in the log buffer
	_, err := r.Execute(req("logs.tail", map[string]interface{}{"lines": 10}))
	if err != nil {
		t.Fatalf("logs.tail error: %v", err)
	}

	entries := d.LogBuffer.Tail("", "", 10)
	for _, e := range entries {
		if e.Message == "logs.tail ok" {
			t.Error("logs.tail should not be recorded in log buffer (observability cap)")
		}
	}
}

func TestRegistryExecute_RecordsAudit(t *testing.T) {
	d := testDepsWithStores(t)
	r := New(d)

	_, err := r.Execute(req("system.info", nil))
	if err != nil {
		t.Fatalf("system.info error: %v", err)
	}

	records := d.AuditStore.List("", "", "", 10)
	if len(records) == 0 {
		t.Fatal("expected at least 1 audit record")
	}
	found := false
	for _, rec := range records {
		if rec.EventType == "capability.call" && rec.Outcome == "ok" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected capability.call ok audit record, got: %+v", records)
	}
}

func TestRegistryExecute_SensitiveFieldsNotInOutput(t *testing.T) {
	d := testDepsWithStores(t)
	r := New(d)

	_, err := r.Execute(req("system.info", nil))
	if err != nil {
		t.Fatalf("system.info error: %v", err)
	}

	// Check that log entries don't contain raw private keys or tokens
	lines := d.LogBuffer.Tail("", "", 10)
	for _, e := range lines {
		if e.Fields != nil {
			if _, ok := e.Fields["privateKey"]; ok {
				t.Error("log entry contains privateKey field")
			}
			if _, ok := e.Fields["adminToken"]; ok {
				t.Error("log entry contains adminToken field")
			}
		}
	}
}
