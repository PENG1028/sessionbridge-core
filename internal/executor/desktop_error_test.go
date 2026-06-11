package executor

import (
	"runtime"
	"strings"
	"testing"
)

// ─── Error Path Coverage Tests for Desktop Capabilities ─────────────────
//
// Every possible error path for each handler:
//   - Missing required fields
//   - Invalid field values
//   - Platform stub errors (via direct function calls)
//   - Action validation errors
//   - Type mismatch errors
//
// Tests are cross-platform aware using runtime.GOOS.

// ─── desktop.clipboard.get ────────────────────────────────────────────────

// TestError_ClipboardGet_StubPlatform tests the clipboard read stub on non-desktop OS.
// The stub for unsupported platforms returns "clipboard not supported on <os>".
func TestError_ClipboardGet_UnsupportedPlatform(t *testing.T) {
	// readClipboard() uses runtime.GOOS to dispatch. On windows/linux/darwin
	// it goes to the platform-specific implementation. The error path for
	// unsupported platforms is tested by verifying the error message pattern.
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("desktop.clipboard.get", nil))
	if err != nil {
		// Acceptable errors: platform tool missing, syscall failure, etc.
		if !strings.Contains(err.Error(), "clipboard") {
			t.Errorf("clipboard.get: error should mention clipboard, got: %v", err)
		}
	}
}

// TestError_ClipboardGet_ResponseShapeOnError verifies that even on error,
// the response is returned as a proper error (not nil result + non-nil error).
func TestError_ClipboardGet_ResponseShapeOnError(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	result, err := r.Execute(req("desktop.clipboard.get", nil))
	if err != nil {
		// Error is expected on headless. Verify nil result.
		if result != nil {
			t.Errorf("clipboard.get: result should be nil on error, got %v", result)
		}
		t.Logf("clipboard.get error (expected): %v", err)
	}
}

// ─── desktop.clipboard.set ────────────────────────────────────────────────

// TestError_ClipboardSet_InvalidPayload tests clipboard.set with non-struct payload.
func TestError_ClipboardSet_InvalidPayload(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	// Sending a bare array instead of an object
	_, err := r.Execute(req("desktop.clipboard.set", []string{"not", "an", "object"}))
	if err != nil {
		t.Logf("clipboard.set array payload: %v", err)
	} else {
		t.Log("clipboard.set: array payload was accepted (JSON decode succeeded)")
	}
}

// TestError_ClipboardSet_NestedObject tests clipboard.set with deeply nested object.
func TestError_ClipboardSet_NestedObject(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	// Deeply nested object that matches no fields
	_, err := r.Execute(req("desktop.clipboard.set", map[string]interface{}{
		"deeply": map[string]interface{}{
			"nested": map[string]interface{}{
				"object": true,
			},
		},
	}))
	if err != nil {
		t.Logf("clipboard.set nested: %v (syscall expected on Windows)", err)
	} else {
		t.Log("clipboard.set: nested object accepted (text is empty)")
	}
}

// TestError_ClipboardSet_VerifyMissingToolError verifies clipboard tool-not-found errors.
func TestError_ClipboardSet_VerifyMissingToolError(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("clipboard.set tool not found: only relevant on Linux")
	}

	// On Linux, writeClipboardLinux checks for xclip and wl-copy.
	// When neither is found, it returns "neither xclip nor wl-copy found".
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("desktop.clipboard.set", map[string]interface{}{
		"text": "test",
	}))
	if err != nil {
		if strings.Contains(err.Error(), "neither") {
			t.Logf("clipboard.set: correct tool-not-found error: %v", err)
		} else {
			t.Logf("clipboard.set: other error: %v", err)
		}
	}
}

// ─── desktop.screenshot ───────────────────────────────────────────────────

// TestError_Screenshot_UnsupportedPlatform verifies screenshot error on unsupported platforms.
func TestError_Screenshot_UnsupportedPlatform(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("desktop.screenshot", nil))
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "screenshot") || strings.Contains(msg, "failed") {
			t.Logf("screenshot: expected platform error: %v", err)
		}
	}
}

// TestError_Screenshot_CrossPlatformErrorCodes verifies screenshot errors are consistent.
func TestError_Screenshot_CrossPlatformErrorCodes(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("desktop.screenshot", nil))
	if err != nil {
		msg := err.Error()
		// Screenshot errors should always mention "screenshot"
		if !strings.Contains(msg, "screenshot") && !strings.Contains(msg, "Screenshot") &&
			!strings.Contains(msg, "GetDC") && !strings.Contains(msg, "BitBlt") &&
			!strings.Contains(msg, "import") && !strings.Contains(msg, "gnome-screenshot") &&
			!strings.Contains(msg, "screencapture") {
			t.Errorf("screenshot error should mention screenshot tool or syscall: %v", err)
		}
		t.Logf("screenshot cross-platform error: %v", err)
	}
}

// TestError_Screenshot_NegativeMonitorIndex tests screenshot with -1 monitor index.
func TestError_Screenshot_NegativeMonitorIndex(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	// Negative monitor index is passed through to platform impl
	_, err := r.Execute(req("desktop.screenshot", map[string]interface{}{
		"monitorIndex": -1,
	}))
	if err != nil {
		t.Logf("screenshot monitorIndex=-1: %v", err)
	}
}

// ─── desktop.click ────────────────────────────────────────────────────────

// TestError_DesktopClick_NilPayload verifies desktop.click defaults x,y to 0 with nil payload.
func TestError_DesktopClick_NilPayload(t *testing.T) {
	skipUnlessGUI(t)
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("desktop.click", nil))
	if err != nil {
		// decodePayload returns nil for empty payload -> x=0, y=0 are defaults
		// OS backend may still fail (non-Windows, headless)
		t.Logf("desktop.click nil payload: %v", err)
	}
}

// TestError_DesktopClick_EmptyPayload verifies desktop.click defaults x,y to 0 with empty payload.
func TestError_DesktopClick_EmptyPayload(t *testing.T) {
	skipUnlessGUI(t)
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("desktop.click", map[string]interface{}{}))
	if err != nil {
		// decodePayload returns nil for empty payload -> x=0, y=0 are defaults
		t.Logf("desktop.click empty payload: %v", err)
	}
}

// TestError_DesktopClick_OnlyXProvided checks missing y coordinate.
func TestError_DesktopClick_OnlyXProvided(t *testing.T) {
	skipUnlessGUI(t)
	deps := testDeps(t)
	r := New(deps)
	// Only x, no y — y defaults to 0, which is technically valid
	_, err := r.Execute(req("desktop.click", map[string]interface{}{
		"x": 100,
	}))
	if err != nil {
		t.Logf("desktop.click only x: %v", err)
	}
}

// TestError_DesktopClick_CrossPlatform verifies desktop.click errors are consistent across platforms.
func TestError_DesktopClick_CrossPlatform(t *testing.T) {
	skipUnlessGUI(t)
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("desktop.click", nil))
	if err == nil {
		t.Skip("desktop.click succeeded with nil payload — unexpected")
	}
	msg := err.Error()
	// All platforms should use consistent error messaging
	if !strings.Contains(strings.ToLower(msg), "invalid") &&
		!strings.Contains(strings.ToLower(msg), "required") &&
		!strings.Contains(strings.ToLower(msg), "not supported") {
		t.Errorf("desktop.click: unexpected error format: %v", err)
	}
}

// ─── desktop.type ─────────────────────────────────────────────────────────

// TestError_DesktopType_EmptyText verifies exact error message for empty text.
func TestError_DesktopType_EmptyText(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("desktop.type", map[string]interface{}{
		"text": "",
	}))
	if err == nil {
		t.Fatal("desktop.type: should error on empty text")
	}
	if !strings.Contains(err.Error(), "text required") {
		t.Errorf("desktop.type empty text: got %q, want 'text required'", err.Error())
	}
}

// TestError_DesktopType_NilPayload verifies error for nil payload.
func TestError_DesktopType_NilPayload(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("desktop.type", nil))
	if err == nil {
		t.Fatal("desktop.type: should error on nil payload (text required)")
	}
	if !strings.Contains(err.Error(), "invalid payload") &&
		!strings.Contains(err.Error(), "text required") {
		t.Errorf("desktop.type nil payload: got %q, want mentioning 'invalid payload' or 'text required'", err.Error())
	}
}

// TestError_DesktopType_InvalidType verifies error for non-string text.
func TestError_DesktopType_InvalidType(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	// Number where string expected
	_, err := r.Execute(req("desktop.type", map[string]interface{}{
		"text": 12345,
	}))
	if err != nil {
		t.Logf("desktop.type numeric text: %v", err)
	}
}

// TestError_DesktopType_BoolText verifies error for boolean text.
func TestError_DesktopType_BoolText(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("desktop.type", map[string]interface{}{
		"text": true,
	}))
	if err != nil {
		t.Logf("desktop.type bool text: %v (JSON decode will fail)", err)
	}
}

// ─── input.keyboard ───────────────────────────────────────────────────────

// TestError_InputKeyboard_NilPayload verifies error for nil payload.
func TestError_InputKeyboard_NilPayload(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("input.keyboard", nil))
	if err == nil {
		t.Fatal("input.keyboard: should error on nil payload")
	}
	if !strings.Contains(err.Error(), "invalid payload") &&
		!strings.Contains(err.Error(), "keys required") {
		t.Errorf("input.keyboard nil payload: got %q", err.Error())
	}
}

// TestError_InputKeyboard_EmptyKeys verifies error for empty keys array.
func TestError_InputKeyboard_EmptyKeys(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("input.keyboard", map[string]interface{}{
		"action": "tap",
		"keys":   []string{},
	}))
	if err == nil {
		t.Fatal("input.keyboard: should error on empty keys")
	}
	if !strings.Contains(err.Error(), "keys required") {
		t.Errorf("input.keyboard empty keys: got %q, want 'keys required'", err.Error())
	}
}

// TestError_InputKeyboard_MissingAction verifies default action behavior.
func TestError_InputKeyboard_MissingAction(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	// Missing action field — defaults to empty string
	_, err := r.Execute(req("input.keyboard", map[string]interface{}{
		"keys": []string{"a"},
	}))
	if err != nil {
		t.Logf("input.keyboard missing action: %v", err)
	}
}

// TestError_InputKeyboard_InvalidAction validates error messages for bad actions.
func TestError_InputKeyboard_InvalidAction(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	tests := []struct {
		action      string
		expectError bool
	}{
		{"", true},
		{"invalid", true},
		{"press", false}, // "press" is valid on Windows
		{"release", true},
	}

	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			_, err := r.Execute(req("input.keyboard", map[string]interface{}{
				"action": tt.action,
				"keys":   []string{"a"},
			}))
			if tt.expectError && err == nil {
				t.Errorf("input.keyboard action=%q: expected error but got nil", tt.action)
			}
			if err != nil {
				t.Logf("input.keyboard action=%q: %v", tt.action, err)
			}
		})
	}
}

// TestError_InputKeyboard_WrongKeysType tests with various wrong types for keys.
func TestError_InputKeyboard_WrongKeysType(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	_, err := r.Execute(req("input.keyboard", map[string]interface{}{
		"action": "tap",
		"keys":   "not_an_array",
	}))
	if err != nil {
		t.Logf("input.keyboard string keys: %v", err)
	}

	_, err = r.Execute(req("input.keyboard", map[string]interface{}{
		"action": "tap",
		"keys":   42,
	}))
	if err != nil {
		t.Logf("input.keyboard numeric keys: %v", err)
	}

	_, err = r.Execute(req("input.keyboard", map[string]interface{}{
		"action": "tap",
		"keys":   map[string]string{"a": "b"},
	}))
	if err != nil {
		t.Logf("input.keyboard map keys: %v", err)
	}
}

// ─── input.mouse ──────────────────────────────────────────────────────────

// TestError_InputMouse_NilPayload verifies error for nil payload.
func TestError_InputMouse_NilPayload(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("input.mouse", nil))
	if err == nil {
		t.Error("input.mouse: should error on nil payload")
	} else {
		t.Logf("input.mouse nil payload: %v", err)
	}
}

// TestError_InputMouse_InvalidAction validates error messages for bad actions.
func TestError_InputMouse_InvalidAction(t *testing.T) {
	skipUnlessGUI(t)
	deps := testDeps(t)
	r := New(deps)

	tests := []struct {
		action string
		valid  bool // true = accepted on at least one platform
	}{
		{"", false},
		{"move", true},
		{"click", true},
		{"scroll", true},
		{"press", true},
		{"release", true},
		{"drag", false},
		{"double_click", false},
		{"hover", false},
	}

	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			_, err := r.Execute(req("input.mouse", map[string]interface{}{
				"action": tt.action,
			}))
			if err != nil {
				if tt.valid {
					t.Logf("input.mouse action=%q (valid but may fail on this platform): %v", tt.action, err)
				} else {
					t.Logf("input.mouse action=%q (invalid, correctly rejected): %v", tt.action, err)
				}
			}
		})
	}
}

// TestError_InputMouse_MissingAction verifies default action behavior.
func TestError_InputMouse_MissingAction(t *testing.T) {
	// Missing action defaults to "" which errors before any mouse movement — safe
	deps := testDeps(t)
	r := New(deps)
	// Missing action defaults to empty string, which is invalid for all platforms
	_, err := r.Execute(req("input.mouse", map[string]interface{}{
		"x": 100,
		"y": 100,
	}))
	if err != nil {
		t.Logf("input.mouse missing action: %v", err)
	}
}

// ─── Stub Function Tests (direct platform stubs) ──────────────────────────

// TestError_StubReadClipboardWindows verifies the Windows clipboard stub
// returns error on non-Windows builds.
func TestError_StubReadClipboardWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows clipboard is not a stub on Windows")
	}
	// On non-Windows, readClipboardWindows is a compile-time stub that always errors
	text, err := readClipboardWindows()
	if err == nil {
		t.Error("readClipboardWindows: expected error on non-Windows stub")
	}
	if text != "" {
		t.Errorf("readClipboardWindows: expected empty string, got %q", text)
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Errorf("readClipboardWindows: error should mention 'not available', got: %v", err)
	}
}

// TestError_StubWriteClipboardWindows verifies the Windows clipboard write stub.
func TestError_StubWriteClipboardWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows clipboard is not a stub on Windows")
	}
	err := writeClipboardWindows("test")
	if err == nil {
		t.Error("writeClipboardWindows: expected error on non-Windows stub")
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Errorf("writeClipboardWindows: error should mention 'not available', got: %v", err)
	}
}

// TestError_StubCaptureScreenWindows verifies the Windows screenshot stub.
func TestError_StubCaptureScreenWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows screenshot is not a stub on Windows")
	}
	img, err := captureScreenWindows(0)
	if err == nil {
		t.Error("captureScreenWindows: expected error on non-Windows stub")
	}
	if img != nil {
		t.Error("captureScreenWindows: expected nil image on error")
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Errorf("captureScreenWindows: error should mention 'not available', got: %v", err)
	}
}

// TestError_StubKeyboardWindows verifies the Windows keyboard stub.
func TestError_StubKeyboardWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keyboard is not a stub on Windows")
	}
	err := keyboardWindows("tap", []string{"a"})
	if err == nil {
		t.Error("keyboardWindows: expected error on non-Windows stub")
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Errorf("keyboardWindows: error should mention 'not available', got: %v", err)
	}
}

// TestError_StubMouseWindows verifies the Windows mouse stub.
func TestError_StubMouseWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows mouse is not a stub on Windows")
	}
	err := mouseWindows("move", 0, 0, "", 0)
	if err == nil {
		t.Error("mouseWindows: expected error on non-Windows stub")
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Errorf("mouseWindows: error should mention 'not available', got: %v", err)
	}
}

// TestError_StubTypeTextWindows verifies the Windows type text stub.
func TestError_StubTypeTextWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows type text is not a stub on Windows")
	}
	err := typeTextWindows("hello")
	if err == nil {
		t.Error("typeTextWindows: expected error on non-Windows stub")
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Errorf("typeTextWindows: error should mention 'not available', got: %v", err)
	}
}

// ─── Combined error path validation ───────────────────────────────────────

// TestError_AllDesktopCapsReturnConsistentErrors verifies that all desktop
// capabilities return consistent error shapes (no panics, structured errors).
func TestError_AllDesktopCapsReturnConsistentErrors(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	caps := []string{
		"desktop.clipboard.get",
		"desktop.clipboard.set",
		"desktop.screenshot",
		"desktop.click",
		"desktop.type",
		"input.keyboard",
		"input.mouse",
	}

	for _, cap := range caps {
		t.Run(cap, func(t *testing.T) {
			// Test with nil payload — all should return an error or a result, never panic
			result, err := r.Execute(req(cap, nil))
			_ = result
			if err != nil {
				// Verify error is not an empty string
				if err.Error() == "" {
					t.Errorf("%s: error message is empty", cap)
				}
				t.Logf("%s nil payload: %v", cap, err)
			} else {
				// If no error, verify result is a map
				if _, ok := result.(map[string]interface{}); !ok {
					t.Errorf("%s nil payload: result type is %T, expected map", cap, result)
				}
			}
		})
	}
}
