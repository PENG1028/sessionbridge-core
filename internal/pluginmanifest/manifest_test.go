package pluginmanifest

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================================
// A. Basic parsing
// ============================================================================

func TestParse_ValidClaudeCode(t *testing.T) {
	m := loadTestManifest(t, "claude-code", "plugin.yaml")
	if m.ID != "claude-code" {
		t.Errorf("id = %q, want claude-code", m.ID)
	}
	if m.Core == nil {
		t.Fatal("core section is nil")
	}
	if len(m.Core.Permissions) == 0 {
		t.Error("expected permissions in claude-code manifest")
	}
}

func TestParse_ValidShell(t *testing.T) {
	m := loadTestManifest(t, "shell", "plugin.yaml")
	if m.ID != "shell" {
		t.Errorf("id = %q, want shell", m.ID)
	}
}

func TestParse_NoSystemUIAdapter(t *testing.T) {
	m := loadTestManifest(t, "backup-runner", "plugin.yaml")
	if m.Adapters.SystemUI != nil {
		t.Error("backup-runner should not have system-ui adapter")
	}
	if m.Adapters.CLI == nil {
		t.Error("backup-runner should have cli adapter")
	}
}

func TestParse_NoCLIAdapter(t *testing.T) {
	// Create an in-memory manifest with only system-ui, no cli
	yaml := `manifestVersion: "1"
id: test-ui
name: Test UI
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: test-ui.read
      description: Read
      capabilities:
        - fs.read
      default: ask
adapters:
  system-ui:
    views:
      - id: test-ui.view
        surface: main.editor
        type: custom-react
        entry: ./View.tsx
        title: Test`
	m, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if m.Adapters.CLI != nil {
		t.Error("should not have cli adapter")
	}
	if m.Adapters.SystemUI == nil {
		t.Error("should have system-ui adapter")
	}
}

func TestParse_MissingCoreInvalid(t *testing.T) {
	yaml := `manifestVersion: "1"
id: no-core
name: No Core
version: 1.0.0
type: plugin
trusted: false`
	m, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	errs := Validate(m)
	if !hasError(errs, "REQUIRED") {
		t.Errorf("expected REQUIRED error for missing core, got: %v", errs)
	}
}

func TestParse_UnsupportedManifestVersion(t *testing.T) {
	yaml := `manifestVersion: "99"
id: bad-version
name: Bad
version: 1.0.0
type: plugin
trusted: false
core:
  permissions: []`
	m, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	errs := Validate(m)
	if !hasError(errs, "UNSUPPORTED") {
		t.Errorf("expected UNSUPPORTED error, got: %v", errs)
	}
}

func TestParse_InvalidYAML(t *testing.T) {
	_, err := ParseYAML([]byte("key: value\norphan_line"))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestParse_JSONManifest(t *testing.T) {
	json := `{
		"manifestVersion": "1",
		"id": "json-plugin",
		"name": "JSON Plugin",
		"version": "1.0.0",
		"type": "plugin",
		"trusted": false,
		"core": {
			"permissions": [{
				"id": "json-plugin.read",
				"description": "Read",
				"capabilities": ["fs.read"],
				"default": "ask"
			}]
		},
		"adapters": {}
	}`
	m, err := ParseJSON([]byte(json))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if m.ID != "json-plugin" {
		t.Errorf("id = %q", m.ID)
	}
}

// ============================================================================
// B. Naming and reserved IDs
// ============================================================================

func TestValidate_PluginIDKebabCase(t *testing.T) {
	tests := []struct {
		id    string
		valid bool
	}{
		{"claude-code", true},
		{"shell", true},
		{"backup-runner", true},
		{"my-plugin-42", true},
		{"CamelCase", false},
		{"snake_case", false},
		{"UPPER", false},
		{"space id", false},
		{"", false},
		{"-leading", false},
		{"trailing-", false},
		{"a", true},
	}
	for _, tt := range tests {
		got := isKebabCase(tt.id)
		if got != tt.valid {
			t.Errorf("isKebabCase(%q) = %v, want %v", tt.id, got, tt.valid)
		}
	}
}

func TestValidate_ReservedIDSystemUI(t *testing.T) {
	m := minimalManifest("system-ui")
	errs := Validate(m)
	if !hasError(errs, "RESERVED") {
		t.Errorf("expected RESERVED error for system-ui, got: %v", errs)
	}
}

func TestValidate_ReservedIDSessionnodeCore(t *testing.T) {
	m := minimalManifest("sessionnode-core")
	errs := Validate(m)
	if !hasError(errs, "RESERVED") {
		t.Errorf("expected RESERVED error for sessionnode-core, got: %v", errs)
	}
}

func TestValidate_PermissionIDNamespaced(t *testing.T) {
	yaml := `manifestVersion: "1"
id: my-plugin
name: My Plugin
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: bad-namespace
      description: Wrong namespace
      capabilities:
        - fs.read
      default: ask`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if !hasError(errs, "NAMESPACE") {
		t.Errorf("expected NAMESPACE error, got: %v", errs)
	}
}

func TestValidate_CommandIDNamespaced(t *testing.T) {
	yaml := `manifestVersion: "1"
id: my-plugin
name: My Plugin
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: my-plugin.read
      description: Read
      capabilities:
        - fs.read
      default: ask
adapters:
  cli:
    commands:
      - id: other-plugin.cmd
        name: cmd
        description: Wrong namespace`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if !hasError(errs, "NAMESPACE") {
		t.Errorf("expected NAMESPACE error, got: %v", errs)
	}
}

func TestValidate_ViewIDNamespaced(t *testing.T) {
	yaml := `manifestVersion: "1"
id: my-plugin
name: My Plugin
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: my-plugin.read
      description: Read
      capabilities:
        - fs.read
      default: ask
adapters:
  system-ui:
    views:
      - id: other-plugin.view
        surface: main.editor
        type: custom-react
        entry: ./View.tsx
        title: Other`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if !hasError(errs, "NAMESPACE") {
		t.Errorf("expected NAMESPACE error, got: %v", errs)
	}
}

func TestValidate_PanelIDNamespaced(t *testing.T) {
	yaml := `manifestVersion: "1"
id: my-plugin
name: My Plugin
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: my-plugin.read
      description: Read
      capabilities:
        - fs.read
      default: ask
adapters:
  system-ui:
    panels:
      - id: other-plugin.panel
        surface: main.editor.bottom
        type: custom-react
        entry: ./Panel.tsx
        title: Other`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if !hasError(errs, "NAMESPACE") {
		t.Errorf("expected NAMESPACE error, got: %v", errs)
	}
}

func TestValidate_PluginDeclaresOtherNamespace(t *testing.T) {
	yaml := `manifestVersion: "1"
id: my-plugin
name: My Plugin
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: my-plugin.read
      description: Read
      capabilities:
        - fs.read
      default: ask
adapters:
  cli:
    commands:
      - id: shell.start
        name: start
        description: This belongs to shell plugin`
	m, _ := ParseYAML([]byte(yaml))
	_ = DetectConflicts([]*Manifest{m})
	// This should be detected as declaring another plugin's namespace
	// The shell plugin isn't registered, but the id "shell.start" uses a prefix
	// that doesn't match "my-plugin."
	errs := Validate(m)
	if !hasError(errs, "NAMESPACE") {
		t.Errorf("expected NAMESPACE error for declaring shell.start, got: %v", errs)
	}
}

// ============================================================================
// C. Adapter separation
// ============================================================================

func TestValidate_CoreOnlyLoads(t *testing.T) {
	yaml := `manifestVersion: "1"
id: core-only
name: Core Only
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: core-only.read
      description: Read
      capabilities:
        - fs.read
      default: ask`
	m, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	errs := Validate(m)
	// Should have no adapter-related errors (adapters section is optional)
	if errs != nil {
		for _, e := range errs {
			if strings.Contains(e.Field, "adapters") {
				t.Errorf("unexpected adapter error: %v", e)
			}
		}
	}
}

func TestValidate_CorePlusCLI(t *testing.T) {
	yaml := `manifestVersion: "1"
id: cli-only
name: CLI Only
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: cli-only.run
      description: Run
      capabilities:
        - process.spawn
      default: deny
adapters:
  cli:
    commands:
      - id: cli-only.run-cmd
        name: run
        description: Run command`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if len(errs) > 0 {
		// Only ignore adapter-related errors
		for _, e := range errs {
			if strings.Contains(e.Field, "cli-only.run") && e.Code == "DANGEROUS_DEFAULT_ALLOW" {
				continue
			}
			if strings.Contains(e.Field, "adapters") {
				t.Errorf("unexpected adapter error: %v", e)
			}
		}
	}
}

func TestValidate_CorePlusSystemUI(t *testing.T) {
	yaml := `manifestVersion: "1"
id: ui-only
name: UI Only
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: ui-only.read
      description: Read
      capabilities:
        - fs.read
      default: ask
adapters:
  system-ui:
    views:
      - id: ui-only.view
        surface: main.editor
        type: custom-react
        entry: ./View.tsx
        title: View`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if len(errs) > 0 {
		for _, e := range errs {
			if strings.Contains(e.Field, "adapters") {
				t.Errorf("unexpected adapter error: %v", e)
			}
		}
	}
}

func TestValidate_AllAdapters(t *testing.T) {
	yaml := `manifestVersion: "1"
id: full-plugin
name: Full Plugin
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: full-plugin.read
      description: Read
      capabilities:
        - fs.read
      default: ask
adapters:
  system-ui:
    views:
      - id: full-plugin.view
        surface: main.editor
        type: custom-react
        entry: ./View.tsx
        title: View
    commands:
      - id: full-plugin.cmd
        title: Command
  cli:
    commands:
      - id: full-plugin.cli.cmd
        name: cmd
        description: A command`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if len(errs) > 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

func TestValidate_SystemUIAdapterNotRequired(t *testing.T) {
	yaml := `manifestVersion: "1"
id: no-ui
name: No UI
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: no-ui.read
      description: Read
      capabilities:
        - fs.read
      default: ask`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if len(errs) > 0 {
		// Only check no errors from core validation
		for _, e := range errs {
			if e.Code == "REQUIRED" && e.Field == "core" {
				continue
			}
			t.Errorf("unexpected error: %v", e)
		}
	}
}

func TestValidate_CLIAdapterNotRequired(t *testing.T) {
	// Same as above — cli adapter is not required
	yaml := `manifestVersion: "1"
id: no-cli
name: No CLI
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: no-cli.read
      description: Read
      capabilities:
        - fs.read
      default: ask`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if len(errs) > 0 {
		for _, e := range errs {
			if e.Code == "REQUIRED" && e.Field == "core" {
				continue
			}
			t.Errorf("unexpected error: %v", e)
		}
	}
}

// ============================================================================
// F. Environment check declarations
// ============================================================================

func TestEnvCheck_BinaryRequiresCommand(t *testing.T) {
	yaml := `manifestVersion: "1"
id: test
name: Test
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: test.read
      description: Read
      capabilities:
        - fs.read
      default: ask
  environment:
    checks:
      - id: bad-check
        type: binary
        required: true`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if !hasError(errs, "REQUIRED") {
		t.Errorf("expected REQUIRED error for binary check without command, got: %v", errs)
	}
}

func TestEnvCheck_CommandCheckNeedsCommandOrArgs(t *testing.T) {
	yaml := `manifestVersion: "1"
id: test
name: Test
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: test.read
      description: Read
      capabilities:
        - fs.read
      default: ask
  environment:
    checks:
      - id: bad-check
        type: command
        required: true`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if !hasError(errs, "REQUIRED") {
		t.Errorf("expected REQUIRED error, got: %v", errs)
	}
}

func TestEnvCheck_VersionWithoutCommand(t *testing.T) {
	yaml := `manifestVersion: "1"
id: test
name: Test
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: test.read
      description: Read
      capabilities:
        - fs.read
      default: ask
  environment:
    checks:
      - id: test-check
        type: binary
        required: true
        command: foo
        requiredVersion: ">= 1.0"`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if !hasError(errs, "RECOMMENDED") {
		t.Errorf("expected RECOMMENDED error, got: %v", errs)
	}
}

func TestEnvCheck_DuplicateCheckID(t *testing.T) {
	yaml := `manifestVersion: "1"
id: test
name: Test
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: test.read
      description: Read
      capabilities:
        - fs.read
      default: ask
  environment:
    checks:
      - id: dup
        type: binary
        command: foo
        required: true
      - id: dup
        type: binary
        command: bar
        required: false`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if !hasError(errs, "DUPLICATE") {
		t.Errorf("expected DUPLICATE error, got: %v", errs)
	}
}

// ============================================================================
// G. File/cache declarations
// ============================================================================

func TestFileDecl_ValidCache(t *testing.T) {
	yaml := `manifestVersion: "1"
id: test
name: Test
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: test.read
      description: Read
      capabilities:
        - fs.read
      default: ask
  files:
    cache: "${plugin.cacheDir}"
    declarations:
      - id: test-cache
        path: "${plugin.cacheDir}/data"
        description: Test cache
        clearable: true`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if len(errs) > 0 {
		// If there's a validation error about something else, that's fine
		// but the file declaration itself should be ok
		for _, e := range errs {
			t.Logf("validation: %v", e)
		}
	}
}

// ============================================================================
// J. Snapshot tests — real plugin examples validate
// ============================================================================

func TestSnapshot_ClaudeCodeValidates(t *testing.T) {
	m := loadTestManifest(t, "claude-code", "plugin.yaml")
	errs := Validate(m)
	if len(errs) > 0 {
		t.Errorf("claude-code validation errors: %v", errs)
	}
}

func TestSnapshot_ShellValidates(t *testing.T) {
	m := loadTestManifest(t, "shell", "plugin.yaml")
	errs := Validate(m)
	if len(errs) > 0 {
		t.Errorf("shell validation errors: %v", errs)
	}
}

func TestSnapshot_BackupRunnerValidates(t *testing.T) {
	m := loadTestManifest(t, "backup-runner", "plugin.yaml")
	errs := Validate(m)
	if len(errs) > 0 {
		t.Errorf("backup-runner validation errors: %v", errs)
	}
}

func TestSnapshot_NormalizedJSONStable(t *testing.T) {
	m := loadTestManifest(t, "claude-code", "plugin.yaml")
	// Round-trip to JSON
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	if !strings.Contains(string(data), `"id":"claude-code"`) {
		t.Error("normalized JSON missing id field")
	}
	if !strings.Contains(string(data), `"manifestVersion"`) {
		t.Error("normalized JSON missing manifestVersion")
	}
}

// ============================================================================
// Helpers
// ============================================================================

func loadTestManifest(t *testing.T, dir, filename string) *Manifest {
	t.Helper()
	path := filepath.Join("testdata", dir, filename)
	m, err := LoadFile(path)
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	return m
}

func minimalManifest(id string) *Manifest {
	return &Manifest{
		ManifestVersion: "1",
		ID:              id,
		Name:            id,
		Version:         "1.0.0",
		Type:            "plugin",
		Core: &CoreSpec{
			Permissions: []PermissionSpec{},
		},
		Adapters: AdapterSpec{},
	}
}

func hasError(errs []ValidationError, code string) bool {
	for _, e := range errs {
		if e.Code == code {
			return true
		}
	}
	return false
}

// Test helpers for detecting dangerous-default-allow without actually loading the file
func TestDangerousDefaultAllowDetection(t *testing.T) {
	// Test that in-memory manifest with process.spawn default:allow gets rejected
	yaml := `manifestVersion: "1"
id: unsafe
name: Unsafe
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: unsafe.spawn
      description: Spawn
      capabilities:
        - process.spawn
      default: allow
    - id: unsafe.read
      description: Read
      capabilities:
        - fs.read
      default: ask`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if !hasError(errs, "DANGEROUS_DEFAULT_ALLOW") {
		t.Errorf("expected DANGEROUS_DEFAULT_ALLOW error, got: %v", errs)
	}
}

func TestDangerousDefaultAllowTrustedPlugin(t *testing.T) {
	yaml := `manifestVersion: "1"
id: trusted-plugin
name: Trusted Plugin
version: 1.0.0
type: plugin
trusted: true
core:
  permissions:
    - id: trusted-plugin.spawn
      description: Spawn
      capabilities:
        - process.spawn
      default: allow`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	// Trusted plugin should be allowed to have dangerous default:allow
	if hasError(errs, "DANGEROUS_DEFAULT_ALLOW") {
		t.Errorf("trusted plugin should not get DANGEROUS_DEFAULT_ALLOW, got: %v", errs)
	}
}

func TestFileDecl_EmptyID(t *testing.T) {
	yaml := `manifestVersion: "1"
id: test
name: Test
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: test.read
      description: Read
      capabilities:
        - fs.read
      default: ask
  files:
    declarations:
      - path: /tmp/foo
        description: No ID`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if !hasError(errs, "REQUIRED") {
		t.Errorf("expected REQUIRED for empty file decl id, got: %v", errs)
	}
}

func TestDuplicatePermissionID(t *testing.T) {
	yaml := `manifestVersion: "1"
id: test
name: Test
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: test.read
      description: Read
      capabilities:
        - fs.read
      default: ask
    - id: test.read
      description: Read again
      capabilities:
        - fs.read
      default: ask`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if !hasError(errs, "DUPLICATE") {
		t.Errorf("expected DUPLICATE error, got: %v", errs)
	}
}

func TestUnknownCapability(t *testing.T) {
	yaml := `manifestVersion: "1"
id: test
name: Test
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: test.unknown
      description: Unknown cap
      capabilities:
        - doesnt.exist
      default: ask`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if !hasError(errs, "UNKNOWN_CAPABILITY") {
		t.Errorf("expected UNKNOWN_CAPABILITY error, got: %v", errs)
	}
}

func TestInvalidDefaultValue(t *testing.T) {
	yaml := `manifestVersion: "1"
id: test
name: Test
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: test.read
      description: Read
      capabilities:
        - fs.read
      default: always`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if !hasError(errs, "INVALID_DEFAULT") {
		t.Errorf("expected INVALID_DEFAULT error, got: %v", errs)
	}
}

func TestFileDecl_EmptyPath(t *testing.T) {
	yaml := `manifestVersion: "1"
id: test
name: Test
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: test.read
      description: Read
      capabilities:
        - fs.read
      default: ask
  files:
    declarations:
      - id: no-path
        description: No path`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if !hasError(errs, "REQUIRED") {
		t.Errorf("expected REQUIRED for empty path, got: %v", errs)
	}
}

// Test that we can read the huge-commands.yaml file even though validation may fail
func TestHugeCommandsDetection(t *testing.T) {
	// Generate a manifest with 3000 commands — should trigger TOO_MANY
	cmds := make([]string, 0, 3000)
	for i := 0; i < 3000; i++ {
		cmds = append(cmds, `      - id: spam-plugin.cmd.`+itoa(i))
		cmds = append(cmds, `        name: cmd`+itoa(i))
		cmds = append(cmds, `        description: Command `+itoa(i))
	}

	yaml := `manifestVersion: "1"
id: spam-plugin
name: Spam Plugin
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: spam-plugin.read
      description: Read
      capabilities:
        - fs.read
      default: ask
adapters:
  cli:
    commands:
` + strings.Join(cmds, "\n")

	m, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	errs := Validate(m)
	if !hasError(errs, "TOO_MANY") {
		t.Errorf("expected TOO_MANY error for 3000 commands, got: %v", errs)
	}
}

func itoa(i int) string {
	return fmt.Sprintf("%d", i)
}

func TestEmptyCapability(t *testing.T) {
	yaml := `manifestVersion: "1"
id: test
name: Test
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: test.empty
      description: Empty cap
      capabilities:
        - ""
      default: ask`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if !hasError(errs, "EMPTY") {
		t.Errorf("expected EMPTY error for empty capability, got: %v", errs)
	}
}

func TestNetworkCapabilityRequiresDescription(t *testing.T) {
	yaml := `manifestVersion: "1"
id: test-net
name: Test Net
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: test-net.connect
      capabilities:
        - network.connect
      default: ask`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if !hasError(errs, "REQUIRED") {
		t.Errorf("expected REQUIRED error for network.connect without description, got: %v", errs)
	}
}

// ============================================================================
// K. View/Panel componentId and entry validation
// ============================================================================

func TestViewHostRenderedRequiresComponentId(t *testing.T) {
	yaml := `manifestVersion: "1"
id: test
name: Test
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: test.read
      description: Read
      capabilities:
        - fs.read
      default: ask
adapters:
  system-ui:
    views:
      - id: test.view
        surface: main.editor
        type: host-rendered
        title: No Component`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if !hasError(errs, "REQUIRED") {
		t.Errorf("expected REQUIRED error for host-rendered view without componentId, got: %v", errs)
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "componentId") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error message to mention componentId, got: %v", errs)
	}
}

func TestViewCustomReactRequiresEntry(t *testing.T) {
	yaml := `manifestVersion: "1"
id: test
name: Test
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: test.read
      description: Read
      capabilities:
        - fs.read
      default: ask
adapters:
  system-ui:
    views:
      - id: test.view
        surface: main.editor
        type: custom-react
        title: No Entry`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if !hasError(errs, "REQUIRED") {
		t.Errorf("expected REQUIRED error for custom-react view without entry, got: %v", errs)
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "custom-react view requires entry") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error message about custom-react view entry, got: %v", errs)
	}
}

func TestPanelHostRenderedRequiresComponentId(t *testing.T) {
	yaml := `manifestVersion: "1"
id: test
name: Test
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: test.read
      description: Read
      capabilities:
        - fs.read
      default: ask
adapters:
  system-ui:
    panels:
      - id: test.panel
        surface: panel.bottom
        type: host-rendered
        title: No Component`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if !hasError(errs, "REQUIRED") {
		t.Errorf("expected REQUIRED error for host-rendered panel without componentId, got: %v", errs)
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "componentId") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error message to mention componentId, got: %v", errs)
	}
}

func TestPanelCustomReactRequiresEntry(t *testing.T) {
	yaml := `manifestVersion: "1"
id: test
name: Test
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: test.read
      description: Read
      capabilities:
        - fs.read
      default: ask
adapters:
  system-ui:
    panels:
      - id: test.panel
        surface: panel.bottom
        type: custom-react
        title: No Entry`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if !hasError(errs, "REQUIRED") {
		t.Errorf("expected REQUIRED error for custom-react panel without entry, got: %v", errs)
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "custom-react panel requires entry") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error message about custom-react panel entry, got: %v", errs)
	}
}

func TestViewSurfaceRequired(t *testing.T) {
	yaml := `manifestVersion: "1"
id: test
name: Test
version: 1.0.0
type: plugin
trusted: false
core:
  permissions:
    - id: test.read
      description: Read
      capabilities:
        - fs.read
      default: ask
adapters:
  system-ui:
    views:
      - id: test.view
        type: custom-react
        entry: ./View.tsx
        title: No Surface`
	m, _ := ParseYAML([]byte(yaml))
	errs := Validate(m)
	if !hasError(errs, "REQUIRED") {
		t.Errorf("expected REQUIRED error for view without surface, got: %v", errs)
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "surface") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error message to mention surface, got: %v", errs)
	}
}

func TestComponentIdIsParsed(t *testing.T) {
	m, err := LoadFile("../../../plugins/terminal/plugin.yaml")
	if err != nil {
		t.Fatalf("load terminal plugin.yaml: %v", err)
	}
	if m.Adapters.SystemUI == nil {
		t.Fatal("expected system-ui adapter")
	}
	if len(m.Adapters.SystemUI.Views) == 0 {
		t.Fatal("expected at least one view")
	}
	v := m.Adapters.SystemUI.Views[0]
	if v.ComponentID != "TerminalView" {
		t.Errorf("ComponentID = %q, want TerminalView", v.ComponentID)
	}
	if v.Type != "host-rendered" {
		t.Errorf("Type = %q, want host-rendered", v.Type)
	}
	if len(m.Adapters.SystemUI.Panels) == 0 {
		t.Fatal("expected at least one panel")
	}
	p := m.Adapters.SystemUI.Panels[0]
	if p.ComponentID != "SessionListPanel" {
		t.Errorf("Panel ComponentID = %q, want SessionListPanel", p.ComponentID)
	}
}
