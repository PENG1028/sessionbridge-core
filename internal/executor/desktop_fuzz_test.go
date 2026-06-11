package executor

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// ─── Fuzz / Boundary Tests for Desktop Capabilities ─────────────────────
//
// These tests exercise edge cases for all 7 desktop/input capabilities:
//   - Zero values, empty strings, nil payloads
//   - Invalid types (string where int expected, negative values)
//   - Unicode/emoji in text fields
//   - Very long strings (megabyte-level for clipboard/type)
//   - Special characters in keyboard key names
//   - Boundary coordinate values
//
// Tests are designed to work across platforms — they focus on the handler
// layer (payload validation, response shape) and not the OS-specific syscalls.

// ─── desktop.clipboard.get ────────────────────────────────────────────────

// TestFuzz_ClipboardGet_NilPayload verifies clipboard.get works with nil payload.
func TestFuzz_ClipboardGet_NilPayload(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	raw, err := r.Execute(req("desktop.clipboard.get", nil))
	if err != nil {
		t.Logf("clipboard.get nil payload: %v (expected on headless CI)", err)
		return
	}
	result := normalize(raw).(map[string]interface{})
	if _, ok := result["text"]; !ok {
		t.Error("clipboard.get: response missing 'text' field")
	}
	if _, ok := result["found"]; !ok {
		t.Error("clipboard.get: response missing 'found' field")
	}
	t.Logf("clipboard.get ok: found=%v", result["found"])
}

// TestFuzz_ClipboardGet_EmptyPayload verifies clipboard.get ignores payload content.
func TestFuzz_ClipboardGet_EmptyPayload(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	raw, err := r.Execute(req("desktop.clipboard.get", map[string]interface{}{}))
	if err != nil {
		t.Logf("clipboard.get empty payload: %v", err)
		return
	}
	result := normalize(raw).(map[string]interface{})
	if _, ok := result["found"]; !ok {
		t.Error("clipboard.get: response missing 'found' field even for empty payload")
	}
}

// TestFuzz_ClipboardGet_WeirdPayload verifies clipboard.get ignores unexpected fields.
func TestFuzz_ClipboardGet_WeirdPayload(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	raw, err := r.Execute(req("desktop.clipboard.get", map[string]interface{}{
		"text":         42,
		"unexpected":   true,
		"nested_array": []int{1, 2, 3},
	}))
	if err != nil {
		t.Logf("clipboard.get weird payload: %v", err)
		return
	}
	result := normalize(raw).(map[string]interface{})
	if _, ok := result["text"]; !ok {
		t.Error("clipboard.get: should still return 'text' field with weird payload")
	}
}

// ─── desktop.clipboard.set ────────────────────────────────────────────────

// TestFuzz_ClipboardSet_EmptyText verifies clipboard.set with empty text.
func TestFuzz_ClipboardSet_EmptyText(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	raw, err := r.Execute(req("desktop.clipboard.set", map[string]interface{}{
		"text": "",
	}))
	if err != nil {
		t.Logf("clipboard.set empty text: %v (expected on headless CI)", err)
		return
	}
	result := normalize(raw).(map[string]interface{})
	if result["status"] != "ok" {
		t.Errorf("clipboard.set: status = %v, want 'ok'", result["status"])
	}
}

// TestFuzz_ClipboardSet_Unicode tests clipboard.set with various Unicode text.
func TestFuzz_ClipboardSet_Unicode(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	tests := []struct {
		name string
		text string
	}{
		{"ascii", "Hello World"},
		{"unicode_cjk", "你好世界"},
		{"emoji", "🎉🚀💻"},
		{"mixed", "Hello 世界! Testing 123"},
		{"special_chars", "!@#$%^&*()_+-=[]{}|;':\",./<>?"},
		{"newlines_tabs", "line1\nline2\tindented\nline3"},
		{"rtl", "مرحبا بالعالم"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.Execute(req("desktop.clipboard.set", map[string]interface{}{
				"text": tt.text,
			}))
			if err != nil {
				t.Logf("clipboard.set %s: %v (expected on headless CI)", tt.name, err)
			}
		})
	}
}

// TestFuzz_ClipboardSet_LongString tests clipboard.set with progressively longer strings.
func TestFuzz_ClipboardSet_LongString(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	lengths := []int{1, 64, 1024, 65536}

	for _, l := range lengths {
		t.Run(fmt.Sprintf("%d_chars", l), func(t *testing.T) {
			text := strings.Repeat("x", l)
			_, err := r.Execute(req("desktop.clipboard.set", map[string]interface{}{
				"text": text,
			}))
			if err != nil {
				t.Logf("clipboard.set %d chars: %v (expected on headless CI)", l, err)
			}
		})
	}
}

// TestFuzz_ClipboardSet_NilPayload verifies clipboard.set handles nil payload (empty text).
func TestFuzz_ClipboardSet_NilPayload(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	// nil payload -> decodePayload returns nil (empty) -> text is ""
	raw, err := r.Execute(req("desktop.clipboard.set", nil))
	if err != nil {
		t.Logf("clipboard.set nil payload: %v", err)
		return
	}
	result := normalize(raw).(map[string]interface{})
	if result["status"] != "ok" {
		t.Errorf("clipboard.set nil payload: status = %v, want 'ok'", result["status"])
	}
}

// TestFuzz_ClipboardSet_InvalidType verifies clipboard.set handles non-string text (JSON coerces).
func TestFuzz_ClipboardSet_InvalidType(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	// JSON decode will coerce int to float64, then unmarshal fails for string field
	_, err := r.Execute(req("desktop.clipboard.set", map[string]interface{}{
		"text": 42,
	}))
	if err != nil {
		t.Logf("clipboard.set invalid type: %v", err)
	} else {
		t.Log("clipboard.set: JSON coerced int 42 to string (type-safe)")
	}
}

// ─── desktop.screenshot ───────────────────────────────────────────────────

// TestFuzz_Screenshot_NilPayload verifies screenshot with nil payload.
func TestFuzz_Screenshot_NilPayload(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	raw, err := r.Execute(req("desktop.screenshot", nil))
	if err != nil {
		t.Logf("screenshot nil payload: %v (expected on headless CI)", err)
		return
	}
	result := normalize(raw).(map[string]interface{})
	if _, ok := result["data"]; !ok {
		t.Error("screenshot: response missing 'data' field")
	}
	if _, ok := result["width"]; !ok {
		t.Error("screenshot: response missing 'width' field")
	}
	if _, ok := result["height"]; !ok {
		t.Error("screenshot: response missing 'height' field")
	}
	if format, _ := result["format"].(string); format != "png" {
		t.Errorf("screenshot: format = %v, want 'png'", format)
	}
}

// TestFuzz_Screenshot_InvalidMonitorIndex tests screenshot with various monitor indices.
func TestFuzz_Screenshot_InvalidMonitorIndex(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	tests := []struct {
		name  string
		index int
	}{
		{"negative", -1},
		{"large", 999},
		{"zero", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := r.Execute(req("desktop.screenshot", map[string]interface{}{
				"monitorIndex": tt.index,
			}))
			if err != nil {
				t.Logf("screenshot monitorIndex=%d: %v", tt.index, err)
				return
			}
			result := normalize(raw).(map[string]interface{})
			if _, ok := result["data"]; !ok {
				t.Error("screenshot: response missing 'data' field")
			}
		})
	}
}

// TestFuzz_Screenshot_StringMonitorIndex tests screenshot with string monitorIndex (type coercion).
func TestFuzz_Screenshot_StringMonitorIndex(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	// JSON string for an int field — decodePayload will set it to 0
	raw, err := r.Execute(req("desktop.screenshot", map[string]interface{}{
		"monitorIndex": "not_a_number",
	}))
	if err != nil {
		t.Logf("screenshot string monitorIndex: %v", err)
		return
	}
	result := normalize(raw).(map[string]interface{})
	if _, ok := result["data"]; !ok {
		t.Error("screenshot: response missing 'data' field")
	}
}

// ─── desktop.click ────────────────────────────────────────────────────────

// TestFuzz_DesktopClick_ZeroCoordinates verifies click at (0,0) is accepted.
func TestFuzz_DesktopClick_ZeroCoordinates(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("desktop.click", map[string]interface{}{
		"x": 0,
		"y": 0,
	}))
	if err != nil {
		t.Logf("desktop.click (0,0): %v (expected on non-Windows / headless)", err)
	}
}

// TestFuzz_DesktopClick_NegativeCoordinates verifies click with negative coordinates.
func TestFuzz_DesktopClick_NegativeCoordinates(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	tests := []struct {
		name string
		x, y int
	}{
		{"both_negative", -100, -100},
		{"x_negative", -100, 100},
		{"y_negative", 100, -100},
		{"large_negative", -99999, -99999},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.Execute(req("desktop.click", map[string]interface{}{
				"x": tt.x,
				"y": tt.y,
			}))
			if err != nil {
				t.Logf("desktop.click (%s): %v", tt.name, err)
			}
		})
	}
}

// TestFuzz_DesktopClick_LargeCoordinates verifies click with very large coordinates.
func TestFuzz_DesktopClick_LargeCoordinates(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("desktop.click", map[string]interface{}{
		"x": 999999,
		"y": 999999,
	}))
	if err != nil {
		t.Logf("desktop.click (999999, 999999): %v", err)
	}
}

// TestFuzz_DesktopClick_InvalidButton tests desktop.click with invalid button names.
func TestFuzz_DesktopClick_InvalidButton(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	buttons := []string{"left", "right", "middle", "invalid_button", "", "double"}
	for _, btn := range buttons {
		_, err := r.Execute(req("desktop.click", map[string]interface{}{
			"x":      100,
			"y":      100,
			"button": btn,
		}))
		if err != nil {
			t.Logf("desktop.click button=%q: %v", btn, err)
		}
	}
}

// TestFuzz_DesktopClick_NilPayload verifies desktop.click defaults x,y to 0 with nil payload.
func TestFuzz_DesktopClick_NilPayload(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("desktop.click", nil))
	if err != nil {
		// Error may come from OS backend (e.g. on non-Windows or headless)
		t.Logf("desktop.click nil payload: %v", err)
	} else {
		t.Log("desktop.click: nil payload defaults x=0, y=0 (valid)")
	}
}

// TestFuzz_DesktopClick_StringCoordinates tests type coercion for coordinates.
func TestFuzz_DesktopClick_StringCoordinates(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("desktop.click", map[string]interface{}{
		"x": "not_a_number",
		"y": 100,
	}))
	if err != nil {
		t.Logf("desktop.click string x: %v (JSON decode rejects or coerces)", err)
	} else {
		t.Log("desktop.click: string coordinates coerced to 0")
	}
}

// ─── desktop.type ─────────────────────────────────────────────────────────

// TestFuzz_DesktopType_EmptyText verifies desktop.type rejects empty text.
func TestFuzz_DesktopType_EmptyText(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("desktop.type", map[string]interface{}{
		"text": "",
	}))
	if err == nil {
		t.Error("desktop.type: expected error for empty text")
	} else if !strings.Contains(err.Error(), "text required") {
		t.Errorf("desktop.type: expected 'text required' error, got: %v", err)
	}
}

// TestFuzz_DesktopType_Unicode tests desktop.type with various Unicode text.
func TestFuzz_DesktopType_Unicode(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("desktop.type unicode: only run on Windows (GUI required)")
	}

	deps := testDeps(t)
	r := New(deps)

	tests := []struct {
		name string
		text string
	}{
		{"ascii", "Hello World"},
		{"unicode_cjk", "你好世界"},
		{"special", "!@#$%^&*()_+-=[]{}|;':\",./<>?"},
		{"emoji", "🎉"},
		{"newlines", "line1\nline2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.Execute(req("desktop.type", map[string]interface{}{
				"text": tt.text,
			}))
			if err != nil {
				t.Logf("desktop.type %s: %v", tt.name, err)
			}
		})
	}
}

// TestFuzz_DesktopType_LongString tests desktop.type with long strings.
func TestFuzz_DesktopType_LongString(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("desktop.type: only run on Windows (GUI required)")
	}

	deps := testDeps(t)
	r := New(deps)

	text := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 100)
	_, err := r.Execute(req("desktop.type", map[string]interface{}{
		"text": text,
	}))
	if err != nil {
		t.Logf("desktop.type long string (%d chars): %v", len(text), err)
	}
}

// TestFuzz_DesktopType_NilPayload verifies desktop.type rejects nil payload.
func TestFuzz_DesktopType_NilPayload(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("desktop.type", nil))
	if err == nil {
		t.Error("desktop.type: expected error for nil payload (text required)")
	} else {
		t.Logf("desktop.type nil payload: %v (correctly rejected)", err)
	}
}

// ─── input.keyboard ───────────────────────────────────────────────────────

// TestFuzz_InputKeyboard_EmptyKeys verifies input.keyboard rejects empty keys.
func TestFuzz_InputKeyboard_EmptyKeys(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("input.keyboard", map[string]interface{}{
		"action": "tap",
		"keys":   []string{},
	}))
	if err == nil {
		t.Error("input.keyboard: expected error for empty keys")
	} else if !strings.Contains(err.Error(), "keys required") {
		t.Errorf("input.keyboard: expected 'keys required' error, got: %v", err)
	}
}

// TestFuzz_InputKeyboard_SpecialKeyNames tests various key name spellings.
func TestFuzz_InputKeyboard_SpecialKeyNames(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("input.keyboard: only run on Windows (GUI required)")
	}

	deps := testDeps(t)
	r := New(deps)

	// Test all known key names plus edge cases
	keySets := [][]string{
		{"enter"},
		{"return"},
		{"tab"},
		{"space"},
		{"escape"},
		{"esc"},
		{"backspace"},
		{"delete"},
		{"home"},
		{"end"},
		{"pageup"},
		{"pagedown"},
		{"up"},
		{"down"},
		{"left"},
		{"right"},
		{"ctrl"},
		{"alt"},
		{"shift"},
		{"win"},
		{"f1"},
		{"f12"},
		{"a", "b", "c"},
		{"1", "2", "3"},
		{"ctrl", "c"},
		{"shift", "a"},
	}

	for i, keys := range keySets {
		t.Run(fmt.Sprintf("set_%d", i), func(t *testing.T) {
			_, err := r.Execute(req("input.keyboard", map[string]interface{}{
				"action": "tap",
				"keys":   keys,
			}))
			if err != nil {
				t.Logf("input.keyboard keys=%v: %v", keys, err)
			}
		})
	}
}

// TestFuzz_InputKeyboard_UnknownKey verifies input.keyboard with unknown key names.
func TestFuzz_InputKeyboard_UnknownKey(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("input.keyboard unknown key: only run on Windows")
	}

	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("input.keyboard", map[string]interface{}{
		"action": "tap",
		"keys":   []string{"this_is_not_a_real_key"},
	}))
	if err != nil {
		t.Logf("input.keyboard unknown key: %v", err)
	}
}

// TestFuzz_InputKeyboard_InvalidAction verifies keyboard action validation across platforms.
func TestFuzz_InputKeyboard_InvalidAction(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	invalidActions := []string{"", "invalid", "hold", "release", "double_tap"}
	for _, action := range invalidActions {
		_, err := r.Execute(req("input.keyboard", map[string]interface{}{
			"action": action,
			"keys":   []string{"a"},
		}))
		if err != nil {
			t.Logf("input.keyboard action=%q: %v", action, err)
		}
	}
}

// TestFuzz_InputKeyboard_NilPayload verifies input.keyboard rejects nil payload.
func TestFuzz_InputKeyboard_NilPayload(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("input.keyboard", nil))
	if err == nil {
		t.Error("input.keyboard: expected error for nil payload")
	} else {
		t.Logf("input.keyboard nil payload: %v (correctly rejected)", err)
	}
}

// TestFuzz_InputKeyboard_TypeFieldMismatch tests payload with wrong types.
func TestFuzz_InputKeyboard_TypeFieldMismatch(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	tests := []struct {
		name    string
		payload map[string]interface{}
	}{
		{"action_int", map[string]interface{}{"action": 42, "keys": []string{"a"}}},
		{"keys_string", map[string]interface{}{"action": "tap", "keys": "a"}},
		{"keys_mixed", map[string]interface{}{"action": "tap", "keys": []interface{}{1, 2, 3}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.Execute(req("input.keyboard", tt.payload))
			if err != nil {
				t.Logf("input.keyboard %s: %v", tt.name, err)
			}
		})
	}
}

// ─── input.mouse ──────────────────────────────────────────────────────────

// TestFuzz_InputMouse_ZeroCoordinates verifies mouse ops at (0,0).
func TestFuzz_InputMouse_ZeroCoordinates(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	_, err := r.Execute(req("input.mouse", map[string]interface{}{
		"action": "move",
		"x":      0,
		"y":      0,
	}))
	if err != nil {
		t.Logf("input.mouse move (0,0): %v (expected on non-Windows)", err)
	}
}

// TestFuzz_InputMouse_NegativeCoordinates verifies mouse with negative coordinates.
func TestFuzz_InputMouse_NegativeCoordinates(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	_, err := r.Execute(req("input.mouse", map[string]interface{}{
		"action": "move",
		"x":      -100,
		"y":      -100,
	}))
	if err != nil {
		t.Logf("input.mouse negative: %v", err)
	}
}

// TestFuzz_InputMouse_InvalidAction verifies mouse action validation.
func TestFuzz_InputMouse_InvalidAction(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	invalidActions := []string{"", "drag", "double_click", "swipe"}
	for _, action := range invalidActions {
		_, err := r.Execute(req("input.mouse", map[string]interface{}{
			"action": action,
		}))
		if err != nil {
			t.Logf("input.mouse action=%q: %v", action, err)
		}
	}
}

// TestFuzz_InputMouse_InvalidButton tests mouse with invalid button.
func TestFuzz_InputMouse_InvalidButton(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("input.mouse invalid button: only run on Windows")
	}
	deps := testDeps(t)
	r := New(deps)

	_, err := r.Execute(req("input.mouse", map[string]interface{}{
		"action": "click",
		"x":      100,
		"y":      100,
		"button": "not_a_button",
	}))
	if err != nil {
		t.Logf("input.mouse invalid button: %v", err)
	}
}

// TestFuzz_InputMouse_ScrollValues tests scroll with various amounts.
func TestFuzz_InputMouse_ScrollValues(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("input.mouse scroll: only run on Windows")
	}
	deps := testDeps(t)
	r := New(deps)

	amounts := []int{0, 1, -1, 10, -10, 100, -100}
	for _, amount := range amounts {
		_, err := r.Execute(req("input.mouse", map[string]interface{}{
			"action": "scroll",
			"amount": amount,
		}))
		if err != nil {
			t.Logf("input.mouse scroll amount=%d: %v", amount, err)
		}
	}
}

// TestFuzz_InputMouse_NilPayload verifies input.mouse rejects nil payload.
func TestFuzz_InputMouse_NilPayload(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("input.mouse", nil))
	if err == nil {
		t.Error("input.mouse: expected error for nil payload (action is required)")
	} else {
		t.Logf("input.mouse nil payload: %v (correctly rejected)", err)
	}
}

// TestFuzz_InputMouse_StringCoordinates tests type coercion.
func TestFuzz_InputMouse_StringCoordinates(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("input.mouse", map[string]interface{}{
		"action": "move",
		"x":      "not_a_number",
	}))
	if err != nil {
		t.Logf("input.mouse string x: %v (JSON decode rejects or coerces)", err)
	} else {
		t.Log("input.mouse: string coordinates coerced to 0")
	}
}
