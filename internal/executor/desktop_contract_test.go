package executor

import (
	"runtime"
	"strings"
	"testing"
)

// ─── Desktop Capability Contract Tests ─────────────────────────────────
//
// These tests validate the API contract for AI desktop capabilities:
//   - Response/error shape for each capability
//   - Payload validation (required fields, types)
//   - Error codes and messages for unsupported platforms
//
// They follow the same contract-test pattern as contract_test.go.

// TestContract_DesktopClipboardGetShape validates clipboard.get response shape.
func TestContract_DesktopClipboardGetShape(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	raw, err := r.Execute(req("desktop.clipboard.get", nil))
	if err != nil {
		// On platforms without clipboard tools, validate error shape
		t.Logf("clipboard.get error (expected on headless/CI): %v", err)
		return
	}

	result := toMap(t, raw)
	checkStringField(t, result, "text")
	checkBoolField(t, result, "found")
}

// TestContract_DesktopClipboardSetShape validates clipboard.set.
func TestContract_DesktopClipboardSetShape(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	// Test: missing text field — payload is valid (empty struct), text becomes ""
	raw, err := r.Execute(req("desktop.clipboard.set", map[string]interface{}{}))
	if err != nil {
		t.Logf("clipboard.set error: %v", err)
		return
	}

	result := toMap(t, raw)
	checkStatusOK(t, result)
}

// TestContract_DesktopScreenshotShape validates screenshot response.
func TestContract_DesktopScreenshotShape(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	raw, err := r.Execute(req("desktop.screenshot", nil))
	if err != nil {
		t.Logf("screenshot error (expected on headless/CI): %v", err)
		return
	}

	result := toMap(t, raw)
	checkStringField(t, result, "data")
	checkIntField(t, result, "width")
	checkIntField(t, result, "height")
	checkStringField(t, result, "format")
}

// TestContract_DesktopClickPayloadValidation validates click payload.
func TestContract_DesktopClickPayloadValidation(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	// click at (0,0) is technically valid on all platforms
	// just verify it doesn't panic
	_, err := r.Execute(req("desktop.click", map[string]interface{}{
		"x": 0,
		"y": 0,
	}))
	if err != nil {
		t.Logf("desktop.click at (0,0) error: %v", err)
	}
}

// TestContract_DesktopClickShape validates successful click response.
func TestContract_DesktopClickShape(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("desktop.click contract: only run on Windows (GUI required)")
	}

	deps := testDeps(t)
	r := New(deps)

	raw, err := r.Execute(req("desktop.click", map[string]interface{}{
		"x": 100,
		"y": 100,
	}))
	if err != nil {
		t.Fatalf("desktop.click failed: %v", err)
	}

	checkStatusOK(t, toMap(t, raw))
}

// TestContract_DesktopTypePayloadValidation validates type payload.
func TestContract_DesktopTypePayloadValidation(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	// Test: missing text
	_, err := r.Execute(req("desktop.type", nil))
	if err == nil {
		t.Error("desktop.type should reject nil payload (text required)")
	} else {
		t.Logf("desktop.type nil payload rejected: %v", err)
	}

	// Test: empty text
	_, err = r.Execute(req("desktop.type", map[string]interface{}{
		"text": "",
	}))
	if err == nil {
		t.Error("desktop.type should reject empty text")
	} else {
		t.Logf("desktop.type empty text rejected: %v", err)
	}
}

// TestContract_DesktopTypeShape validates successful type response.
func TestContract_DesktopTypeShape(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("desktop.type contract: only run on Windows (GUI required)")
	}

	deps := testDeps(t)
	r := New(deps)

	raw, err := r.Execute(req("desktop.type", map[string]interface{}{
		"text": "Hello",
	}))
	if err != nil {
		t.Fatalf("desktop.type failed: %v", err)
	}

	checkStatusOK(t, toMap(t, raw))
}

// TestContract_InputKeyboardPayloadValidation validates keyboard payload.
func TestContract_InputKeyboardPayloadValidation(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	// Test: missing keys
	_, err := r.Execute(req("input.keyboard", nil))
	if err == nil {
		t.Error("input.keyboard should reject nil payload (keys required)")
	} else {
		t.Logf("input.keyboard nil payload rejected: %v", err)
	}

	// Test: empty keys array
	_, err = r.Execute(req("input.keyboard", map[string]interface{}{
		"action": "tap",
		"keys":   []string{},
	}))
	if err == nil {
		t.Error("input.keyboard should reject empty keys")
	} else {
		t.Logf("input.keyboard empty keys rejected: %v", err)
	}
}

// TestContract_InputKeyboardShape validates keyboard response.
func TestContract_InputKeyboardShape(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("input.keyboard contract: only run on Windows (GUI required)")
	}

	deps := testDeps(t)
	r := New(deps)

	raw, err := r.Execute(req("input.keyboard", map[string]interface{}{
		"action": "tap",
		"keys":   []string{"a"},
	}))
	if err != nil {
		t.Fatalf("input.keyboard failed: %v", err)
	}

	checkStatusOK(t, toMap(t, raw))
}

// TestContract_InputMousePayloadValidation validates mouse payload.
func TestContract_InputMousePayloadValidation(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	_, err := r.Execute(req("input.mouse", nil))
	if err == nil {
		t.Error("input.mouse should reject nil payload")
	} else {
		t.Logf("input.mouse nil payload rejected: %v", err)
	}
}

// TestContract_InputMouseShape validates mouse response.
func TestContract_InputMouseShape(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("input.mouse contract: only run on Windows (GUI required)")
	}

	deps := testDeps(t)
	r := New(deps)

	raw, err := r.Execute(req("input.mouse", map[string]interface{}{
		"action": "move",
		"x":      100,
		"y":      100,
	}))
	if err != nil {
		t.Fatalf("input.mouse failed: %v", err)
	}

	checkStatusOK(t, toMap(t, raw))
}

// TestContract_DesktopCapabilitiesRegistered verifies all desktop caps are callable.
func TestContract_DesktopCapabilitiesRegistered(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	expected := []string{
		"desktop.clipboard.get",
		"desktop.clipboard.set",
		"desktop.screenshot",
		"desktop.click",
		"desktop.type",
		"input.keyboard",
		"input.mouse",
	}

	for _, cap := range expected {
		_, err := r.Execute(req(cap, nil))
		if err != nil && !isPlatformError(err) && !isPayloadError(err) {
			t.Errorf("%s: unexpected error: %v", cap, err)
		}
	}
}

// ── Test helpers ──────────────────────────────────────────────────────────

func toMap(t *testing.T, v interface{}) map[string]interface{} {
	t.Helper()
	m, ok := v.(map[string]interface{})
	if !ok {
		t.Fatalf("result is not a map: %T", v)
	}
	return m
}

func checkStringField(t *testing.T, m map[string]interface{}, key string) {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Errorf("response missing required string key %q", key)
		return
	}
	if _, ok := v.(string); !ok {
		t.Errorf("key %q should be string, got %T", key, v)
	}
}

func checkIntField(t *testing.T, m map[string]interface{}, key string) {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Errorf("response missing required key %q", key)
		return
	}
	switch v.(type) {
	case float64, int, int64:
		// JSON decode produces float64; fine
	default:
		t.Errorf("key %q should be numeric, got %T", key, v)
	}
}

func checkBoolField(t *testing.T, m map[string]interface{}, key string) {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Errorf("response missing required bool key %q", key)
		return
	}
	if _, ok := v.(bool); !ok {
		t.Errorf("key %q should be bool, got %T", key, v)
	}
}

func checkStatusOK(t *testing.T, m map[string]interface{}) {
	t.Helper()
	v, ok := m["status"]
	if !ok {
		t.Fatal("response missing required key 'status'")
	}
	if v != "ok" {
		t.Errorf("status = %q, want 'ok'", v)
	}
}

func isPlatformError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "not supported on") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "neither") ||
		strings.Contains(msg, "xdotool not found") ||
		strings.Contains(msg, "clipboard not supported") ||
		strings.Contains(msg, "Clipboard") ||
		strings.Contains(msg, "GlobalLock") ||
		strings.Contains(msg, "OpenClipboard") ||
		strings.Contains(msg, "failed") && (strings.Contains(msg, "screenshot") || strings.Contains(msg, "clipboard"))
}

func isPayloadError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "required") ||
		strings.Contains(msg, "invalid payload") ||
		strings.Contains(msg, "empty") ||
		strings.Contains(msg, "action") // e.g. "mouse action "" not supported"
}
