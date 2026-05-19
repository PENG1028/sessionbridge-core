package logs

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Logger tests ---

func TestLogger_WritesJSONLines(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, LevelDebug, "test-app")

	log.Info("hello world")
	log.Debug("debug msg")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	for i, line := range lines {
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Errorf("line %d: not valid JSON: %v", i, err)
			continue
		}
		if entry["app"] != "test-app" {
			t.Errorf("line %d: expected app=test-app, got %v", i, entry["app"])
		}
		if _, ok := entry["ts"]; !ok {
			t.Errorf("line %d: missing ts", i)
		}
	}
}

func TestLogger_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, LevelInfo, "test")

	log.Debug("should be filtered")
	log.Info("should appear")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line (Info), got %d: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "should appear") {
		t.Errorf("expected Info message, got: %s", lines[0])
	}
}

func TestLogger_ErrorLevel(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, LevelError, "test")

	log.Info("should be filtered")
	log.Warn("should be filtered too")
	log.Error("should appear")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line (Error), got %d: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "should appear") {
		t.Errorf("expected Error message, got: %s", lines[0])
	}
}

func TestLogger_FieldsInOutput(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, LevelDebug, "test")

	log.Info("request", F("method", "GET"), F("path", "/api/v1"))

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}

	fields, ok := entry["fields"].(map[string]interface{})
	if !ok {
		t.Fatal("expected fields object in output")
	}
	if fields["method"] != "GET" {
		t.Errorf("expected method=GET, got %v", fields["method"])
	}
	if fields["path"] != "/api/v1" {
		t.Errorf("expected path=/api/v1, got %v", fields["path"])
	}
}

func TestLogger_SetLevel(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, LevelError, "test")

	log.Debug("nope")
	log.SetLevel(LevelDebug)
	log.Debug("yes")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line after SetLevel, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "yes") {
		t.Errorf("expected debug message after SetLevel, got: %s", lines[0])
	}
}

func TestLogger_DefaultLevel(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, "invalid-level", "test")
	// Should fallback to Info

	log.Debug("nope")
	log.Info("yes")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "yes") {
		t.Errorf("expected Info message after fallback, got: %s", lines[0])
	}
}

func TestLogger_NoFields(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, LevelDebug, "test")

	log.Info("no fields here")

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if _, exists := entry["fields"]; exists {
		t.Errorf("expected no 'fields' key when none provided, got: %v", entry["fields"])
	}
}

func TestLogger_TimestampFormat(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, LevelDebug, "test")

	log.Info("check ts")

	var entry struct {
		TS string `json:"ts"`
	}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	// RFC3339 format: "2026-05-19T10:00:00Z" or with timezone offset.
	if !strings.Contains(entry.TS, "T") || (!strings.HasSuffix(entry.TS, "Z") && len(entry.TS) < 20) {
		t.Errorf("ts does not look like RFC3339: %q", entry.TS)
	}
}

// --- AuditEntry tests ---

func TestAuditEntry_Serialization(t *testing.T) {
	entry := AuditEntry{
		Timestamp:  1715000000000,
		PluginID:   "test-plugin",
		ActorType:  "user",
		ActorID:    "alice",
		Capability: "fs.read",
		TargetNode: "node-1",
		Allowed:    true,
		Detail:     "allowed by policy",
		RequestID:  "req-123",
	}

	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded AuditEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.PluginID != "test-plugin" {
		t.Errorf("expected pluginId=test-plugin, got %s", decoded.PluginID)
	}
	if decoded.Allowed != true {
		t.Errorf("expected allowed=true, got %v", decoded.Allowed)
	}
	if decoded.RequestID != "req-123" {
		t.Errorf("expected requestId=req-123, got %s", decoded.RequestID)
	}
	if decoded.TargetNode != "node-1" {
		t.Errorf("expected targetNode=node-1, got %s", decoded.TargetNode)
	}
}

func TestAuditEntry_SerializationOmitsEmptyFields(t *testing.T) {
	entry := AuditEntry{
		Timestamp:  1715000000000,
		PluginID:   "test-plugin",
		ActorType:  "system",
		ActorID:    "daemon",
		Capability: "process.spawn",
		Allowed:    false,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := raw["targetNode"]; ok {
		t.Errorf("expected omitempty targetNode to be absent")
	}
	if _, ok := raw["detail"]; ok {
		t.Errorf("expected omitempty detail to be absent")
	}
	if _, ok := raw["requestId"]; ok {
		t.Errorf("expected omitempty requestId to be absent")
	}
}

// --- AuditLogger tests ---

func TestAuditLogger_Log(t *testing.T) {
	var buf bytes.Buffer
	audit := NewAuditLogger(&nopWriteCloser{&buf})

	audit.Log(AuditEntry{
		PluginID:   "p1",
		ActorType:  "user",
		ActorID:    "bob",
		Capability: "network.connect",
		Allowed:    true,
	})

	var entry AuditEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if entry.PluginID != "p1" {
		t.Errorf("expected pluginId=p1, got %s", entry.PluginID)
	}
	if entry.Allowed != true {
		t.Errorf("expected allowed=true, got %v", entry.Allowed)
	}
	if entry.Timestamp == 0 {
		t.Errorf("expected non-zero timestamp")
	}
}

func TestAuditLogger_LogSetsDefaultTimestamp(t *testing.T) {
	var buf bytes.Buffer
	audit := NewAuditLogger(&nopWriteCloser{&buf})

	// Entry with zero timestamp should auto-assign.
	audit.Log(AuditEntry{PluginID: "p1", ActorType: "system", ActorID: "d", Capability: "x", Allowed: false})

	var entry AuditEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if entry.Timestamp == 0 {
		t.Error("expected auto-assigned timestamp, got 0")
	}
}

func TestAuditLogger_Close(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	audit := NewAuditLogger(f)
	audit.Log(AuditEntry{PluginID: "p1", ActorType: "system", ActorID: "d", Capability: "x", Allowed: false})

	if err := audit.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// File should exist and contain at least one line.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after close: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty audit file after close")
	}
}

// --- RotateWriter tests ---

func TestRotateWriter_CreatesFileAndWrites(t *testing.T) {
	dir := t.TempDir()
	w, err := NewRotateWriter(dir, "test.log", 1024*1024, 3)
	if err != nil {
		t.Fatalf("NewRotateWriter: %v", err)
	}
	defer w.Close()

	n, err := w.Write([]byte("hello\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 6 {
		t.Errorf("expected 6 bytes written, got %d", n)
	}

	data, err := os.ReadFile(filepath.Join(dir, "test.log"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "hello\n" {
		t.Errorf("expected 'hello\\n', got %q", string(data))
	}
}

func TestRotateWriter_Rotation(t *testing.T) {
	dir := t.TempDir()
	// Use a tiny maxSize so every write triggers rotation.
	w, err := NewRotateWriter(dir, "rotate.log", 5, 3)
	if err != nil {
		t.Fatalf("NewRotateWriter: %v", err)
	}
	defer w.Close()

	// Write "AAAA\n" (5 bytes) — fits within maxSize=5 (written becomes 5).
	_, err = w.Write([]byte("AAAA\n"))
	if err != nil {
		t.Fatalf("write 1: %v", err)
	}

	// Write "BBBB\n" (5 bytes) — this pushes written to 10 >= maxSize, triggers rotation.
	_, err = w.Write([]byte("BBBB\n"))
	if err != nil {
		t.Fatalf("write 2: %v", err)
	}

	// After first rotation:
	//   rotate.log     = "BBBB\n"  (fresh file after rotation)
	//   rotate.log.1   = "AAAA\n"  (rotated current)
	// Check contents.
	current := readFile(t, dir, "rotate.log")
	if current != "BBBB\n" {
		t.Errorf("expected current file 'BBBB\\n', got %q", current)
	}
	gen1 := readFile(t, dir, "rotate.log.1")
	if gen1 != "AAAA\n" {
		t.Errorf("expected rotate.log.1 'AAAA\\n', got %q", gen1)
	}

	// Write "CCCC\n" — triggers another rotation.
	_, err = w.Write([]byte("CCCC\n"))
	if err != nil {
		t.Fatalf("write 3: %v", err)
	}

	// After second rotation:
	//   rotate.log     = "CCCC\n"
	//   rotate.log.1   = "BBBB\n"
	//   rotate.log.2   = "AAAA\n"
	current = readFile(t, dir, "rotate.log")
	if current != "CCCC\n" {
		t.Errorf("expected current file 'CCCC\\n', got %q", current)
	}
	gen1 = readFile(t, dir, "rotate.log.1")
	if gen1 != "BBBB\n" {
		t.Errorf("expected rotate.log.1 'BBBB\\n', got %q", gen1)
	}
	gen2 := readFile(t, dir, "rotate.log.2")
	if gen2 != "AAAA\n" {
		t.Errorf("expected rotate.log.2 'AAAA\\n', got %q", gen2)
	}

	// Write "DDDD\n" — triggers third rotation; old .2 shifts into .3 (maxFiles=3).
	_, err = w.Write([]byte("DDDD\n"))
	if err != nil {
		t.Fatalf("write 4: %v", err)
	}

	// After third rotation:
	//   rotate.log     = "DDDD\n"
	//   rotate.log.1   = "CCCC\n"
	//   rotate.log.2   = "BBBB\n"
	//   rotate.log.3   = "AAAA\n"  (old .2 shifted up; maxFiles=3 keeps 3 generations)
	current = readFile(t, dir, "rotate.log")
	if current != "DDDD\n" {
		t.Errorf("expected current file 'DDDD\\n', got %q", current)
	}
	gen1 = readFile(t, dir, "rotate.log.1")
	if gen1 != "CCCC\n" {
		t.Errorf("expected rotate.log.1 'CCCC\\n', got %q", gen1)
	}
	gen2 = readFile(t, dir, "rotate.log.2")
	if gen2 != "BBBB\n" {
		t.Errorf("expected rotate.log.2 'BBBB\\n', got %q", gen2)
	}
	gen3 := readFile(t, dir, "rotate.log.3")
	if gen3 != "AAAA\n" {
		t.Errorf("expected rotate.log.3 'AAAA\\n', got %q", gen3)
	}

	// rotate.log.4 should not exist (beyond maxFiles).
	if _, err := os.Stat(filepath.Join(dir, "rotate.log.4")); err == nil {
		t.Errorf("rotate.log.4 should not exist beyond maxFiles=3")
	}
}

func TestRotateWriter_SeedsWrittenFromExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seed.log")

	// Pre-populate with 8 bytes.
	if err := os.WriteFile(path, []byte("ABCD\nEF\n"), 0644); err != nil {
		t.Fatalf("pre-write: %v", err)
	}

	w, err := NewRotateWriter(dir, "seed.log", 10, 2)
	if err != nil {
		t.Fatalf("NewRotateWriter: %v", err)
	}
	defer w.Close()

	// Writing 3 more bytes takes total to 11 >= 10, triggers rotation.
	_, err = w.Write([]byte("GH\n"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	// After rotation, seed.log should contain only "GH\n".
	current := readFile(t, dir, "seed.log")
	if current != "GH\n" {
		t.Errorf("expected current 'GH\\n', got %q", current)
	}
	// seed.log.1 should contain the original 8 bytes.
	gen1 := readFile(t, dir, "seed.log.1")
	if gen1 != "ABCD\nEF\n" {
		t.Errorf("expected gen1 'ABCD\\nEF\\n', got %q", gen1)
	}
}

func TestRotateWriter_MaxFilesZero(t *testing.T) {
	dir := t.TempDir()
	w, err := NewRotateWriter(dir, "zero.log", 5, 0)
	if err != nil {
		t.Fatalf("NewRotateWriter: %v", err)
	}
	defer w.Close()

	_, _ = w.Write([]byte("AAAA\n"))
	_, _ = w.Write([]byte("BBBB\n"))

	// maxFiles=0 means no rotated files are kept; only current should exist.
	current := readFile(t, dir, "zero.log")
	if current != "BBBB\n" {
		t.Errorf("expected current 'BBBB\\n', got %q", current)
	}
	// zero.log.0 (or .1) should not exist — they'd be named .1 as min.
	matches, err := filepath.Glob(filepath.Join(dir, "zero.log.*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("expected no rotated files with maxFiles=0, got %v", matches)
	}
}

// --- Setup helper test ---

func TestSetup_CreatesCoreAndAudit(t *testing.T) {
	dir := t.TempDir()

	logger, audit, err := Setup(dir, LevelDebug)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if logger == nil {
		t.Error("expected non-nil logger")
	}
	if audit == nil {
		t.Error("expected non-nil audit logger")
	}

	// Both loggers write something and close.
	logger.Info("core startup")
	audit.Log(AuditEntry{
		PluginID:   "system",
		ActorType:  "system",
		ActorID:    "init",
		Capability: "core.startup",
		Allowed:    true,
	})

	// Close the rotate writers through the audit logger and by closing core's writer.
	audit.Close()
	if rw, ok := logger.writer.(*RotateWriter); ok {
		rw.Close()
	}

	// Check files exist.
	corePath := filepath.Join(dir, "logs", "core.log")
	auditPath := filepath.Join(dir, "logs", "audit.log")

	if _, err := os.Stat(corePath); os.IsNotExist(err) {
		t.Errorf("core.log not created at %s", corePath)
	}
	if _, err := os.Stat(auditPath); os.IsNotExist(err) {
		t.Errorf("audit.log not created at %s", auditPath)
	}
}

func TestSetup_InvalidDir(t *testing.T) {
	// Passing an empty string for dataDir should work (creates /logs in CWD)
	// but let's avoid polluting. Instead pass a path with a null byte to force error.
	_, _, err := Setup("\x00invalid", LevelDebug)
	if err == nil {
		t.Skip("platform may not reject null-byte paths; skipping")
	}
}

func TestLevelSeverity(t *testing.T) {
	tests := []struct {
		level    string
		expected int
	}{
		{LevelDebug, 0},
		{LevelInfo, 1},
		{LevelWarn, 2},
		{LevelError, 3},
		{"unknown", 1}, // fallback
	}
	for _, tt := range tests {
		got := severity(tt.level)
		if got != tt.expected {
			t.Errorf("severity(%q) = %d, want %d", tt.level, got, tt.expected)
		}
	}
}

func TestF_Helper(t *testing.T) {
	f := F("key", "val")
	if f.Key != "key" {
		t.Errorf("expected Key=key, got %s", f.Key)
	}
	if f.Value != "val" {
		t.Errorf("expected Value=val, got %v", f.Value)
	}
}

// --- Helpers ---

// nopWriteCloser wraps a bytes.Buffer so it satisfies io.WriteCloser.
type nopWriteCloser struct {
	*bytes.Buffer
}

func (n *nopWriteCloser) Close() error { return nil }

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}
