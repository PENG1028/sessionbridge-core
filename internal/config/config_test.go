package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Load
// ---------------------------------------------------------------------------

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	jsonData := `{
		"core": {
			"listenAddr": ":9999",
			"dataDir": "/tmp/testdata",
			"log": {
				"level": "debug",
				"maxSize": 50,
				"maxFiles": 5
			}
		},
		"node": {
			"name": "test-node",
			"role": "relay"
		}
	}`
	if err := os.WriteFile(configPath, []byte(jsonData), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	mgr := NewManager(configPath)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg := mgr.Get()

	if cfg.Core.ListenAddr != ":9999" {
		t.Errorf("ListenAddr = %q, want %q", cfg.Core.ListenAddr, ":9999")
	}
	if cfg.Core.DataDir != "/tmp/testdata" {
		t.Errorf("DataDir = %q, want %q", cfg.Core.DataDir, "/tmp/testdata")
	}
	if cfg.Core.Log.Level != "debug" {
		t.Errorf("Log.Level = %q, want %q", cfg.Core.Log.Level, "debug")
	}
	if cfg.Core.Log.MaxSize != 50 {
		t.Errorf("Log.MaxSize = %d, want %d", cfg.Core.Log.MaxSize, 50)
	}
	if cfg.Core.Log.MaxFiles != 5 {
		t.Errorf("Log.MaxFiles = %d, want %d", cfg.Core.Log.MaxFiles, 5)
	}
	if cfg.Node.Name != "test-node" {
		t.Errorf("Node.Name = %q, want %q", cfg.Node.Name, "test-node")
	}
	if cfg.Node.Role != "relay" {
		t.Errorf("Node.Role = %q, want %q", cfg.Node.Role, "relay")
	}
}

func TestLoadDefaultsAreAppliedWhenFieldMissing(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	// Partial config — only sets listenAddr, everything else should get defaults.
	jsonData := `{"core": {"listenAddr": ":1234"}}`
	if err := os.WriteFile(configPath, []byte(jsonData), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	mgr := NewManager(configPath)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg := mgr.Get()

	// Explicitly set.
	if cfg.Core.ListenAddr != ":1234" {
		t.Errorf("ListenAddr = %q, want %q", cfg.Core.ListenAddr, ":1234")
	}
	// Defaults.
	if cfg.Core.Log.Level != "info" {
		t.Errorf("Log.Level = %q, want %q", cfg.Core.Log.Level, "info")
	}
	if cfg.Core.Log.MaxSize != 100 {
		t.Errorf("Log.MaxSize = %d, want %d", cfg.Core.Log.MaxSize, 100)
	}
	if cfg.Core.Log.MaxFiles != 10 {
		t.Errorf("Log.MaxFiles = %d, want %d", cfg.Core.Log.MaxFiles, 10)
	}
	if cfg.Node.Role != "standalone" {
		t.Errorf("Node.Role = %q, want %q", cfg.Node.Role, "standalone")
	}
}

func TestLoadMissingFileCreatesWithDefaults(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	mgr := NewManager(configPath)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// File should now exist.
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("config file was not created")
	}

	cfg := mgr.Get()
	if cfg.Core.ListenAddr != ":9090" {
		t.Errorf("ListenAddr = %q, want %q", cfg.Core.ListenAddr, ":9090")
	}
	if cfg.Core.Log.Level != "info" {
		t.Errorf("Log.Level = %q, want %q", cfg.Core.Log.Level, "info")
	}
}

// ---------------------------------------------------------------------------
// Save + round-trip
// ---------------------------------------------------------------------------

func TestSaveThenLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	mgr := NewManager(configPath)
	if err := mgr.Load(); err != nil {
		t.Fatalf("initial Load: %v", err)
	}

	// Modify via Set.
	if err := mgr.Set("core.listenAddr", ":7070"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := mgr.Set("node.name", "roundtrip-node"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := mgr.Set("node.role", "leaf"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := mgr.Set("core.log.level", "warn"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Save.
	if err := mgr.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load into a fresh manager.
	mgr2 := NewManager(configPath)
	if err := mgr2.Load(); err != nil {
		t.Fatalf("second Load: %v", err)
	}

	cfg := mgr2.Get()
	if cfg.Core.ListenAddr != ":7070" {
		t.Errorf("ListenAddr = %q, want %q", cfg.Core.ListenAddr, ":7070")
	}
	if cfg.Node.Name != "roundtrip-node" {
		t.Errorf("Node.Name = %q, want %q", cfg.Node.Name, "roundtrip-node")
	}
	if cfg.Node.Role != "leaf" {
		t.Errorf("Node.Role = %q, want %q", cfg.Node.Role, "leaf")
	}
	if cfg.Core.Log.Level != "warn" {
		t.Errorf("Log.Level = %q, want %q", cfg.Core.Log.Level, "warn")
	}
}

// ---------------------------------------------------------------------------
// Dot-notation Set
// ---------------------------------------------------------------------------

func TestSetCoreListenAddr(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "config.json"))
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := mgr.Set("core.listenAddr", ":5050"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	cfg := mgr.Get()
	if cfg.Core.ListenAddr != ":5050" {
		t.Errorf("ListenAddr = %q, want %q", cfg.Core.ListenAddr, ":5050")
	}
}

func TestSetNestedStructField(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "config.json"))
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := mgr.Set("core.log.level", "error"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := mgr.Set("core.log.maxSize", 200); err != nil {
		t.Fatalf("Set: %v", err)
	}

	cfg := mgr.Get()
	if cfg.Core.Log.Level != "error" {
		t.Errorf("Log.Level = %q, want %q", cfg.Core.Log.Level, "error")
	}
	if cfg.Core.Log.MaxSize != 200 {
		t.Errorf("Log.MaxSize = %d, want %d", cfg.Core.Log.MaxSize, 200)
	}
}

func TestSetPluginPermissions(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "config.json"))
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Set a permission grant via dot-notation.
	if err := mgr.Set("plugin.permissions.myPlugin.fs.mode", "allow"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := mgr.Set("plugin.permissions.myPlugin.fs.constraints.maxSize", "10MB"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	cfg := mgr.Get()
	if cfg.Plugin.Permissions == nil {
		t.Fatal("Permissions map is nil")
	}

	inner, ok := cfg.Plugin.Permissions["myPlugin"]
	if !ok {
		t.Fatal(`missing key "myPlugin"`)
	}
	grant, ok := inner["fs"]
	if !ok {
		t.Fatal(`missing key "fs"`)
	}
	if grant.Mode != "allow" {
		t.Errorf("Mode = %q, want %q", grant.Mode, "allow")
	}
	if grant.Constraints == nil {
		t.Fatal("Constraints map is nil")
	}
	if grant.Constraints["maxSize"] != "10MB" {
		t.Errorf(`Constraints["maxSize"] = %v, want "10MB"`, grant.Constraints["maxSize"])
	}
}

func TestSetInvalidField(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "config.json"))
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	err := mgr.Set("core.nonexistent", "value")
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestSetEmptyKey(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "config.json"))
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	err := mgr.Set("", "value")
	if err == nil {
		t.Fatal("expected error for empty key")
	}
}

// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Get concurrency / copy safety
// ---------------------------------------------------------------------------

func TestGetReturnsCopy(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "config.json"))
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := mgr.Set("core.listenAddr", ":8080"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	cfg1 := mgr.Get()
	cfg2 := mgr.Get()

	// Mutating cfg1 should not affect cfg2 or the manager.
	cfg1.Core.ListenAddr = ":3000"

	if mgr.Get().Core.ListenAddr != ":8080" {
		t.Error("mutating returned Config mutated internal state")
	}
	if cfg2.Core.ListenAddr != ":8080" {
		t.Error("mutating cfg1 mutated cfg2")
	}
}

// ---------------------------------------------------------------------------
// Type conversion in Set
// ---------------------------------------------------------------------------

func TestSetTypeConversions(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "config.json"))
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// float64 → int (common when JSON-unmarshalled values flow into Set)
	if err := mgr.Set("core.log.maxSize", float64(75)); err != nil {
		t.Fatalf("Set float64→int: %v", err)
	}
	cfg := mgr.Get()
	if cfg.Core.Log.MaxSize != 75 {
		t.Errorf("Log.MaxSize = %d, want %d", cfg.Core.Log.MaxSize, 75)
	}

	// string → int (parsed)
	if err := mgr.Set("core.log.maxFiles", "8"); err != nil {
		t.Fatalf("Set string→int: %v", err)
	}
	cfg = mgr.Get()
	if cfg.Core.Log.MaxFiles != 8 {
		t.Errorf("Log.MaxFiles = %d, want %d", cfg.Core.Log.MaxFiles, 8)
	}

	// int → string
	if err := mgr.Set("node.name", 42); err != nil {
		t.Fatalf("Set int→string: %v", err)
	}
	cfg = mgr.Get()
	if cfg.Node.Name != "42" {
		t.Errorf("Node.Name = %q, want %q", cfg.Node.Name, "42")
	}
}

// ---------------------------------------------------------------------------
// Defaults helper
// ---------------------------------------------------------------------------

func TestDefaultConfigHasExpectedValues(t *testing.T) {
	cfg := defaultConfig()

	if cfg.Core.ListenAddr != ":9090" {
		t.Errorf("ListenAddr = %q, want %q", cfg.Core.ListenAddr, ":9090")
	}
	if cfg.Core.Log.Level != "info" {
		t.Errorf("Log.Level = %q, want %q", cfg.Core.Log.Level, "info")
	}
	if cfg.Core.Log.MaxSize != 100 {
		t.Errorf("Log.MaxSize = %d, want %d", cfg.Core.Log.MaxSize, 100)
	}
	if cfg.Core.Log.MaxFiles != 10 {
		t.Errorf("Log.MaxFiles = %d, want %d", cfg.Core.Log.MaxFiles, 10)
	}
	if cfg.Node.Role != "standalone" {
		t.Errorf("Node.Role = %q, want %q", cfg.Node.Role, "standalone")
	}
	if cfg.Core.DataDir == "" {
		t.Error("DataDir should not be empty")
	}
	if strings.Contains(cfg.Core.DataDir, "~") {
		t.Errorf("DataDir should expand home dir, got %q", cfg.Core.DataDir)
	}
}

// ---------------------------------------------------------------------------
// JSON round-trip: verify the file written by Save is valid and decodable
// ---------------------------------------------------------------------------

func TestSaveProducesValidJSON(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	mgr := NewManager(configPath)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := mgr.Set("core.listenAddr", ":1234"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := mgr.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Read file and verify it's valid JSON.
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("saved file is empty")
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("saved file is not valid JSON: %v", err)
	}

	// Verify our value made it.
	core, ok := decoded["core"].(map[string]interface{})
	if !ok {
		t.Fatal("root 'core' key missing or not an object")
	}
	addr, ok := core["listenAddr"].(string)
	if !ok {
		t.Fatal("core.listenAddr missing or not a string")
	}
	if addr != ":1234" {
		t.Errorf("core.listenAddr = %q, want %q", addr, ":1234")
	}

	// Verify revision was persisted.
	rev, ok := decoded["_revision"]
	if !ok {
		t.Fatal("saved file missing _revision field")
	}
	revFloat, ok := rev.(float64)
	if !ok {
		t.Fatalf("_revision is not a number: %T", rev)
	}
	if int64(revFloat) < 1 {
		t.Errorf("_revision = %v, want >= 1", revFloat)
	}
}

// ---------------------------------------------------------------------------
// Revision-based concurrency control
// ---------------------------------------------------------------------------

func TestSetWithRevisionOK(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "config.json"))
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// First set bumps revision to 1.
	if err := mgr.Set("core.listenAddr", ":9090"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// SetWithRevision with expectedRevision=1 should succeed.
	if err := mgr.SetWithRevision("core.listenAddr", ":8080", 1); err != nil {
		t.Fatalf("SetWithRevision: %v", err)
	}

	cfg := mgr.Get()
	if cfg.Core.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want %q", cfg.Core.ListenAddr, ":8080")
	}
}

func TestSetWithRevisionConflict(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "config.json"))
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// First set bumps revision to 1.
	if err := mgr.Set("core.listenAddr", ":9090"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Second set bumps revision to 2.
	if err := mgr.Set("node.name", "v2"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Try SetWithRevision with expectedRevision=1 (stale) → should fail.
	err := mgr.SetWithRevision("core.listenAddr", ":8080", 1)
	if err == nil {
		t.Fatal("expected ConfigConflictError, got nil")
	}
	conflict, ok := err.(*ConfigConflictError)
	if !ok {
		t.Fatalf("expected *ConfigConflictError, got %T: %v", err, err)
	}
	if conflict.ExpectedRevision != 1 {
		t.Errorf("ExpectedRevision = %d, want 1", conflict.ExpectedRevision)
	}
	if conflict.ActualRevision != 2 {
		t.Errorf("ActualRevision = %d, want 2", conflict.ActualRevision)
	}
}

func TestSetWithRevisionZeroBypassesCheck(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(filepath.Join(dir, "config.json"))
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// expectedRevision=0 should bypass the check.
	if err := mgr.SetWithRevision("core.listenAddr", ":7070", 0); err != nil {
		t.Fatalf("SetWithRevision(0): %v", err)
	}

	// Second call with 0 should also work.
	if err := mgr.SetWithRevision("node.name", "force-set", 0); err != nil {
		t.Fatalf("SetWithRevision(0) again: %v", err)
	}

	cfg := mgr.Get()
	if cfg.Core.ListenAddr != ":7070" {
		t.Errorf("ListenAddr = %q, want %q", cfg.Core.ListenAddr, ":7070")
	}
	if cfg.Node.Name != "force-set" {
		t.Errorf("Node.Name = %q, want %q", cfg.Node.Name, "force-set")
	}
}
