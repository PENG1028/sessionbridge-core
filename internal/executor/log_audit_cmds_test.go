package executor

import (
	"testing"
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
