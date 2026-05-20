package pluginmanifest

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// writePluginYAML writes a plugin.yaml file at dir/<id>/plugin.yaml.
func writePluginYAML(t *testing.T, dir, id, content string) {
	t.Helper()
	pdir := filepath.Join(dir, id)
	if err := os.MkdirAll(pdir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "plugin.yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestPluginRegistry_MultiDirScan(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	writePluginYAML(t, dir1, "plugin-a", `
manifestVersion: "1"
id: plugin-a
name: Plugin A
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: plugin-a.read
      description: Read
      capabilities:
        - fs.read
`)
	writePluginYAML(t, dir2, "plugin-b", `
manifestVersion: "1"
id: plugin-b
name: Plugin B
version: 2.0.0
type: plugin
trusted: true
core:
  permissions:
    - id: plugin-b.write
      description: Write
      capabilities:
        - fs.write
`)

	reg := NewPluginRegistry([]string{dir1, dir2}, nil)
	plugins := reg.ListPlugins()

	if len(plugins) != 2 {
		t.Fatalf("expected 2 plugins, got %d", len(plugins))
	}

	// Sort for deterministic assertion.
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].ID < plugins[j].ID })

	if plugins[0].ID != "plugin-a" || plugins[0].Version != "1.0.0" {
		t.Errorf("plugin-a mismatch: %+v", plugins[0])
	}
	if plugins[0].Trusted {
		t.Errorf("plugin-a should not be trusted")
	}
	if plugins[1].ID != "plugin-b" || plugins[1].Version != "2.0.0" {
		t.Errorf("plugin-b mismatch: %+v", plugins[1])
	}
	if !plugins[1].Trusted {
		t.Errorf("plugin-b should be trusted")
	}
	if !plugins[1].Enabled {
		t.Errorf("plugin-b should be enabled")
	}
}

func TestPluginRegistry_DuplicatePluginID(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	// Same plugin ID in both directories — first one wins.
	writePluginYAML(t, dir1, "dup-plugin", `
manifestVersion: "1"
id: dup-plugin
name: Original
version: 1.0.0
type: plugin
core:
  permissions:
    - id: dup-plugin.read
      description: Read
      capabilities:
        - fs.read
`)
	writePluginYAML(t, dir2, "dup-plugin", `
manifestVersion: "1"
id: dup-plugin
name: Duplicate
version: 2.0.0
type: plugin
core:
  permissions:
    - id: dup-plugin.write
      description: Write
      capabilities:
        - fs.write
`)

	reg := NewPluginRegistry([]string{dir1, dir2}, nil)
	plugins := reg.ListPlugins()

	if len(plugins) != 1 {
		t.Fatalf("expected 1 plugin (first wins), got %d", len(plugins))
	}
	if plugins[0].Name != "Original" {
		t.Errorf("expected Original to win, got %q", plugins[0].Name)
	}
	if plugins[0].Version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %q", plugins[0].Version)
	}
}

func TestPluginRegistry_DisabledPlugin(t *testing.T) {
	dir := t.TempDir()

	writePluginYAML(t, dir, "enabled-plugin", `
manifestVersion: "1"
id: enabled-plugin
name: Enabled
version: 1.0.0
type: plugin
core:
  permissions:
    - id: enabled-plugin.read
      description: Read
      capabilities:
        - fs.read
`)
	writePluginYAML(t, dir, "disabled-plugin", `
manifestVersion: "1"
id: disabled-plugin
name: Disabled
version: 1.0.0
type: plugin
core:
  permissions:
    - id: disabled-plugin.read
      description: Read
      capabilities:
        - fs.read
`)

	reg := NewPluginRegistry([]string{dir}, []string{"disabled-plugin"})

	if !reg.PluginEnabled("enabled-plugin") {
		t.Error("enabled-plugin should be enabled")
	}
	if reg.PluginEnabled("disabled-plugin") {
		t.Error("disabled-plugin should be disabled")
	}

	plugins := reg.ListPlugins()
	if len(plugins) != 2 {
		t.Fatalf("expected 2 plugins, got %d", len(plugins))
	}
	for _, p := range plugins {
		if p.ID == "disabled-plugin" && p.Enabled {
			t.Error("disabled-plugin should report enabled=false")
		}
		if p.ID == "enabled-plugin" && !p.Enabled {
			t.Error("enabled-plugin should report enabled=true")
		}
	}
}

func TestPluginRegistry_LoadManifest(t *testing.T) {
	dir := t.TempDir()

	writePluginYAML(t, dir, "test-plugin", `
manifestVersion: "1"
id: test-plugin
name: Test Plugin
version: 3.0.0
type: plugin
trusted: true
description: A test plugin
author: SessionNode
core:
  permissions:
    - id: test-plugin.read
      description: Read something
      capabilities:
        - fs.read
        - fs.list
  environment:
    checks:
      - id: check-bash
        type: binary
        required: true
        command: bash
        installHint: Install bash
  files:
    config: "${plugin.configDir}"
    data: "${plugin.dataDir}"
    declarations:
      - id: test-cache
        path: "${plugin.cacheDir}/data"
        description: Cache data
        clearable: true
`)

	reg := NewPluginRegistry([]string{dir}, nil)

	// LoadManifest should return the manifest.
	m, err := reg.LoadManifest("test-plugin")
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Name != "Test Plugin" {
		t.Errorf("name = %q, want %q", m.Name, "Test Plugin")
	}
	if m.Version != "3.0.0" {
		t.Errorf("version = %q, want %q", m.Version, "3.0.0")
	}
	if !m.Trusted {
		t.Error("trusted should be true")
	}
	if m.Description != "A test plugin" {
		t.Errorf("description = %q", m.Description)
	}

	// Environment checks.
	if m.Core == nil {
		t.Fatal("core is nil")
	}
	if len(m.Core.Environment.Checks) != 1 {
		t.Fatalf("expected 1 env check, got %d", len(m.Core.Environment.Checks))
	}
	check := m.Core.Environment.Checks[0]
	if check.ID != "check-bash" || check.Command != "bash" || !check.Required {
		t.Errorf("env check mismatch: %+v", check)
	}

	// File declarations.
	if len(m.Core.Files.Declarations) != 1 {
		t.Fatalf("expected 1 file decl, got %d", len(m.Core.Files.Declarations))
	}
	decl := m.Core.Files.Declarations[0]
	if decl.ID != "test-cache" || !decl.Clearable {
		t.Errorf("file decl mismatch: %+v", decl)
	}
}

func TestPluginRegistry_LoadManifest_NotFound(t *testing.T) {
	dir := t.TempDir()
	reg := NewPluginRegistry([]string{dir}, nil)

	m, err := reg.LoadManifest("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent plugin")
	}
	if m != nil {
		t.Fatal("expected nil manifest")
	}
}

func TestPluginRegistry_FormatErrorNoCrash(t *testing.T) {
	dir := t.TempDir()

	// Invalid YAML — should not crash, plugin should appear with error.
	writePluginYAML(t, dir, "bad-plugin", `
manifestVersion: "1"
id: bad-plugin
name: Bad
version: 1.0.0
type: plugin
core:
  permissions:
    - id: bad-plugin
      capabilities:
        - unknown: capability
`)

	// Also put a valid plugin alongside to ensure scan continues past errors.
	writePluginYAML(t, dir, "good-plugin", `
manifestVersion: "1"
id: good-plugin
name: Good
version: 1.0.0
type: plugin
core:
  permissions:
    - id: good-plugin.read
      description: Read
      capabilities:
        - fs.read
`)

	reg := NewPluginRegistry([]string{dir}, nil)
	plugins := reg.ListPlugins()

	// Both should appear — bad plugin with an error, good without.
	foundBad := false
	foundGood := false
	for _, p := range plugins {
		if p.ID == "bad-plugin" {
			foundBad = true
			if p.Error == "" {
				t.Error("bad-plugin should have a non-empty Error field")
			}
			// LoadManifest should return error.
			if _, err := reg.LoadManifest("bad-plugin"); err == nil {
				t.Error("LoadManifest for bad-plugin should return error")
			}
		}
		if p.ID == "good-plugin" {
			foundGood = true
			if p.Error != "" {
				t.Errorf("good-plugin should have no error, got %q", p.Error)
			}
			// LoadManifest should succeed.
			if _, err := reg.LoadManifest("good-plugin"); err != nil {
				t.Errorf("LoadManifest for good-plugin: %v", err)
			}
		}
	}
	if !foundBad {
		t.Error("bad-plugin not found in listing")
	}
	if !foundGood {
		t.Error("good-plugin not found in listing")
	}
}

func TestPluginRegistry_FormatErrorUnmarshal(t *testing.T) {
	dir := t.TempDir()

	// Unparseable YAML — malformed content.
	pdir := filepath.Join(dir, "corrupt")
	if err := os.MkdirAll(pdir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "plugin.yaml"), []byte("{{{{{corrupt"), 0644); err != nil {
		t.Fatal(err)
	}

	// This should not panic.
	reg := NewPluginRegistry([]string{dir}, nil)
	plugins := reg.ListPlugins()

	if len(plugins) != 1 {
		t.Fatalf("expected 1 plugin (corrupt), got %d", len(plugins))
	}
	if plugins[0].ID != "corrupt" {
		t.Errorf("expected plugin 'corrupt', got %q", plugins[0].ID)
	}
	if plugins[0].Error == "" {
		t.Error("corrupt plugin should have an error message")
	}

	// LoadManifest should return error.
	if _, err := reg.LoadManifest("corrupt"); err == nil {
		t.Error("LoadManifest for corrupt plugin should return error")
	}
}

func TestPluginRegistry_EmptyDirs(t *testing.T) {
	// Non-existent directory — should not crash.
	reg := NewPluginRegistry([]string{"/nonexistent/path"}, nil)
	if len(reg.ListPlugins()) != 0 {
		t.Error("expected no plugins from nonexistent dir")
	}
}

func TestPluginRegistry_CapabilityMap(t *testing.T) {
	dir := t.TempDir()

	writePluginYAML(t, dir, "cap-test", `
manifestVersion: "1"
id: cap-test
name: Cap Test
version: 1.0.0
type: plugin
core:
  permissions:
    - id: cap-test.read
      description: Read
      capabilities:
        - fs.read
        - fs.list
    - id: cap-test.write
      description: Write
      capabilities:
        - fs.write
`)

	reg := NewPluginRegistry([]string{dir}, nil)
	caps := reg.CapabilityMap()

	capList, ok := caps["cap-test"]
	if !ok {
		t.Fatal("cap-test not in capability map")
	}

	expected := []string{"fs.read", "fs.list", "fs.write"}
	if len(capList) != len(expected) {
		t.Fatalf("expected %d capabilities, got %d: %v", len(expected), len(capList), capList)
	}
	for _, c := range expected {
		found := false
		for _, got := range capList {
			if got == c {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("capability %q not found in map", c)
		}
	}
}

func TestPluginRegistry_DisabledPluginNotInCapMap(t *testing.T) {
	dir := t.TempDir()

	writePluginYAML(t, dir, "enabled-caps", `
manifestVersion: "1"
id: enabled-caps
name: Enabled Caps
version: 1.0.0
type: plugin
core:
  permissions:
    - id: enabled-caps.read
      description: Read
      capabilities:
        - fs.read
`)
	writePluginYAML(t, dir, "disabled-caps", `
manifestVersion: "1"
id: disabled-caps
name: Disabled Caps
version: 1.0.0
type: plugin
core:
  permissions:
    - id: disabled-caps.read
      description: Read
      capabilities:
        - fs.write
`)

	reg := NewPluginRegistry([]string{dir}, []string{"disabled-caps"})
	caps := reg.CapabilityMap()

	if _, ok := caps["disabled-caps"]; ok {
		t.Error("disabled plugin should not appear in capability map")
	}
	if _, ok := caps["enabled-caps"]; !ok {
		t.Error("enabled plugin should appear in capability map")
	}
}

func TestPluginRegistry_ScanDirsDefault(t *testing.T) {
	// When configured dirs are empty, ScanDirs should return at least one entry.
	dirs := ScanDirs(nil)
	if len(dirs) == 0 {
		t.Error("ScanDirs(nil) should return at least one default dir")
	}
}

func TestPluginRegistry_ValidationErrors(t *testing.T) {
	dir := t.TempDir()

	// Invalid: missing core section.
	writePluginYAML(t, dir, "no-core", `
manifestVersion: "1"
id: no-core
name: No Core
version: 1.0.0
type: plugin
`)

	reg := NewPluginRegistry([]string{dir}, nil)

	// Should have validation errors.
	errs := reg.ValidationErrors("no-core")
	if len(errs) == 0 {
		t.Fatal("expected validation errors for no-core plugin")
	}

	foundRequired := false
	for _, e := range errs {
		if e.Code == "REQUIRED" && e.Field == "core" {
			foundRequired = true
			break
		}
	}
	if !foundRequired {
		t.Errorf("expected REQUIRED error for core field, got: %+v", errs)
	}

	// Plugin should still appear in listing with error.
	plugins := reg.ListPlugins()
	for _, p := range plugins {
		if p.ID == "no-core" && p.Error == "" {
			t.Error("no-core should have a non-empty error")
		}
	}
}

func TestPluginRegistry_AllConflicts(t *testing.T) {
	dir := t.TempDir()

	// Two plugins, same CLI command ID (but should be fine since they're different plugins).
	writePluginYAML(t, dir, "alpha", `
manifestVersion: "1"
id: alpha
name: Alpha
version: 1.0.0
type: plugin
core:
  permissions:
    - id: alpha.read
      description: Read
      capabilities:
        - fs.read
adapters:
  cli:
    commands:
      - id: alpha.run
        name: run
        description: Run alpha
`)
	writePluginYAML(t, dir, "beta", `
manifestVersion: "1"
id: beta
name: Beta
version: 1.0.0
type: plugin
core:
  permissions:
    - id: beta.read
      description: Read
      capabilities:
        - fs.read
adapters:
  cli:
    commands:
      - id: beta.run
        name: run
        description: Run beta
`)

	reg := NewPluginRegistry([]string{dir}, nil)
	conflicts := reg.AllConflicts()
	if len(conflicts) > 0 {
		t.Errorf("expected 0 conflicts, got %d: %+v", len(conflicts), conflicts)
	}
}

func TestPluginRegistry_PluginEnabled(t *testing.T) {
	dir := t.TempDir()
	writePluginYAML(t, dir, "test-me", `
manifestVersion: "1"
id: test-me
name: Test
version: 1.0.0
type: plugin
core:
  permissions:
    - id: test-me.read
      description: Read
      capabilities:
        - fs.read
`)

	// Enabled.
	reg := NewPluginRegistry([]string{dir}, nil)
	if !reg.PluginEnabled("test-me") {
		t.Error("plugin should be enabled by default")
	}

	// Unknown plugin.
	if reg.PluginEnabled("unknown") {
		t.Error("unknown plugin should return false")
	}

	// Disabled.
	reg2 := NewPluginRegistry([]string{dir}, []string{"test-me"})
	if reg2.PluginEnabled("test-me") {
		t.Error("plugin should be disabled")
	}
}

func TestPluginRegistry_NoManifestDir(t *testing.T) {
	// A directory exists but has no plugin.yaml or plugin.json inside — skip silently.
	dir := t.TempDir()
	emptyDir := filepath.Join(dir, "empty")
	if err := os.MkdirAll(emptyDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Write a valid plugin in the root dir.
	writePluginYAML(t, dir, "real-plugin", `
manifestVersion: "1"
id: real-plugin
name: Real
version: 1.0.0
type: plugin
core:
  permissions:
    - id: real-plugin.read
      description: Read
      capabilities:
        - fs.read
`)

	reg := NewPluginRegistry([]string{dir}, nil)
	plugins := reg.ListPlugins()
	if len(plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(plugins))
	}
	if plugins[0].ID != "real-plugin" {
		t.Errorf("expected real-plugin, got %q", plugins[0].ID)
	}
}
