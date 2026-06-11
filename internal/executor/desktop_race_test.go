package executor

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
)

// ─── Race / Concurrency Tests for Desktop Capabilities ──────────────────
//
// These tests:
//   - Call the same capability from multiple goroutines simultaneously
//   - Verify no panics, no data races (must be run with -race flag)
//   - Test registry concurrency safety
//   - Test with t.Parallel() where appropriate
//
// Run with: go test -race ./internal/executor/ -run "TestRace_" -count=1

// ─── desktop.clipboard.get ────────────────────────────────────────────────

// TestRace_ClipboardGet_Concurrent tests clipboard.get with 10 goroutines.
func TestRace_ClipboardGet_Concurrent(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	var wg sync.WaitGroup
	errs := make([]error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			result, err := r.Execute(req("desktop.clipboard.get", nil))
			if err != nil {
				errs[idx] = err
			}
			if result != nil {
				if m, ok := result.(map[string]interface{}); ok {
					if f, ok := m["found"]; ok {
						_ = f
					}
				}
			}
		}(i)
	}
	wg.Wait()

	errorCount := 0
	for _, e := range errs {
		if e != nil {
			errorCount++
		}
	}
	if errorCount > 0 {
		t.Logf("clipboard.get: %d/%d goroutines had errors (expected on headless CI)", errorCount, len(errs))
	}
}

// TestRace_ClipboardSet_Concurrent tests clipboard.set from 10 goroutines.
func TestRace_ClipboardSet_Concurrent(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	var wg sync.WaitGroup
	errs := make([]error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			text := fmt.Sprintf("goroutine-%d", idx)
			result, err := r.Execute(req("desktop.clipboard.set", map[string]interface{}{
				"text": text,
			}))
			if err != nil {
				errs[idx] = err
			}
			_ = result
		}(i)
	}
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			t.Logf("clipboard.set goroutine %d: %v", i, e)
		}
	}
}

// ─── desktop.screenshot ───────────────────────────────────────────────────

// TestRace_Screenshot_SequentialParallel tests multiple screenshots in parallel.
func TestRace_Screenshot_SequentialParallel(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("screenshot race: only run on Windows")
	}
	skipUnlessGUI(t)
	deps := testDeps(t)
	r := New(deps)

	var wg sync.WaitGroup
	results := make([]interface{}, 5)
	errs := make([]error, 5)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			result, err := r.Execute(req("desktop.screenshot", nil))
			results[idx] = result
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	successCount := 0
	for i, e := range errs {
		if e != nil {
			t.Logf("screenshot goroutine %d: %v", i, e)
		} else if results[i] != nil {
			successCount++
		}
	}
	t.Logf("screenshot: %d/5 succeeded concurrently", successCount)
}

// ─── desktop.click ────────────────────────────────────────────────────────

// TestRace_DesktopClick_Concurrent tests concurrent click operations.
func TestRace_DesktopClick_Concurrent(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("desktop.click race: only run on Windows")
	}
	skipUnlessGUI(t)
	deps := testDeps(t)
	r := New(deps)

	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := r.Execute(req("desktop.click", map[string]interface{}{
				"x": 100 + idx*10,
				"y": 100 + idx*10,
			}))
			if err != nil {
				t.Logf("desktop.click goroutine %d: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()
}

// ─── desktop.type ─────────────────────────────────────────────────────────

// TestRace_DesktopType_Concurrent tests concurrent text typing.
func TestRace_DesktopType_Concurrent(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("desktop.type race: only run on Windows")
	}
	skipUnlessGUI(t)
	deps := testDeps(t)
	r := New(deps)

	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := r.Execute(req("desktop.type", map[string]interface{}{
				"text": fmt.Sprintf("Goroutine %d typing", idx),
			}))
			if err != nil {
				t.Logf("desktop.type goroutine %d: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()
}

// ─── input.keyboard ───────────────────────────────────────────────────────

// TestRace_InputKeyboard_Concurrent tests concurrent keystrokes.
func TestRace_InputKeyboard_Concurrent(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("input.keyboard race: only run on Windows")
	}
	skipUnlessGUI(t)
	deps := testDeps(t)
	r := New(deps)

	var wg sync.WaitGroup

	keys := []string{"a", "b", "c", "d", "e"}
	for _, key := range keys {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			_, err := r.Execute(req("input.keyboard", map[string]interface{}{
				"action": "tap",
				"keys":   []string{k},
			}))
			if err != nil {
				t.Logf("input.keyboard key=%s: %v", k, err)
			}
		}(key)
	}
	wg.Wait()
}

// ─── input.mouse ──────────────────────────────────────────────────────────

// TestRace_InputMouse_Concurrent tests concurrent mouse operations.
func TestRace_InputMouse_Concurrent(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("input.mouse race: only run on Windows")
	}
	skipUnlessGUI(t)
	deps := testDeps(t)
	r := New(deps)

	var wg sync.WaitGroup

	actions := []struct {
		action string
		x, y   int
	}{
		{"move", 100, 100},
		{"move", 200, 200},
		{"move", 300, 300},
		{"move", 400, 400},
	}

	for _, a := range actions {
		wg.Add(1)
		go func(action string, x, y int) {
			defer wg.Done()
			_, err := r.Execute(req("input.mouse", map[string]interface{}{
				"action": action,
				"x":      x,
				"y":      y,
			}))
			if err != nil {
				t.Logf("input.mouse %s(%d,%d): %v", action, x, y, err)
			}
		}(a.action, a.x, a.y)
	}
	wg.Wait()
}

// ─── Mixed Capability Concurrent Access ───────────────────────────────────

// TestRace_MixedDesktopCaps_Concurrent tests all 7 capabilities concurrently.
func TestRace_MixedDesktopCaps_Concurrent(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("mixed desktop caps race: only run on Windows")
	}
	skipUnlessGUI(t)
	deps := testDeps(t)
	r := New(deps)

	var wg sync.WaitGroup

	// Spawn goroutines for each capability
	caps := []struct {
		name    string
		payload interface{}
	}{
		{"desktop.clipboard.get", nil},
		{"desktop.clipboard.set", map[string]interface{}{"text": "mixed-race-test"}},
		{"desktop.screenshot", nil},
		{"desktop.click", map[string]interface{}{"x": 150, "y": 150}},
		{"desktop.type", map[string]interface{}{"text": "mixed"}},
		{"input.keyboard", map[string]interface{}{"action": "tap", "keys": []string{"a"}}},
		{"input.mouse", map[string]interface{}{"action": "move", "x": 200, "y": 200}},
	}

	// Run each 3 times concurrently
	for _, cap := range caps {
		for j := 0; j < 3; j++ {
			wg.Add(1)
			go func(capName string, payload interface{}) {
				defer wg.Done()
				_, err := r.Execute(req(capName, payload))
				if err != nil {
					// Log but don't fail — platform errors expected
					t.Logf("mixed %s: %v", capName, err)
				}
			}(cap.name, cap.payload)
		}
	}
	wg.Wait()
}

// ─── Registry Concurrency Safety ──────────────────────────────────────────

// TestRace_RegistryConcurrentAccess tests concurrent Execute calls on the registry.
func TestRace_RegistryConcurrentAccess(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	var wg sync.WaitGroup
	panicCh := make(chan string, 1000)

	// Use a mix of registered and edge-case capabilities to stress the handler map
	capabilities := []string{
		"desktop.clipboard.get",
		"desktop.clipboard.set",
		"desktop.screenshot",
		"desktop.click",
		"desktop.type",
		"input.keyboard",
		"input.mouse",
		"node.health",
		"system.info",
		"nonexistent.capability", // should error, not panic
	}

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicCh <- fmt.Sprintf("panic in goroutine %d: %v", idx, r)
				}
			}()
			capName := capabilities[idx%len(capabilities)]
			_, err := r.Execute(req(capName, nil))
			_ = err // errors are ok, panics are not
		}(i)
	}
	wg.Wait()
	close(panicCh)

	for msg := range panicCh {
		t.Error(msg)
	}
}

// ─── t.Parallel() Tests ───────────────────────────────────────────────────

// TestRace_Parallel_ClipboardReadAccess tests parallel clipboard read safety.
func TestRace_Parallel_ClipboardReadAccess(t *testing.T) {
	t.Parallel()
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("desktop.clipboard.get", nil))
	if err != nil {
		t.Logf("clipboard.get parallel: %v", err)
	}
}

// TestRace_Parallel_SystemInfo tests parallel system.info access.
func TestRace_Parallel_SystemInfo(t *testing.T) {
	t.Parallel()
	deps := testDeps(t)
	r := New(deps)
	m := execOK(t, r, "system.info", nil)
	if m["os"] == nil {
		t.Error("missing os field")
	}
}

// TestRace_Parallel_NodeHealth tests parallel node.health access.
func TestRace_Parallel_NodeHealth(t *testing.T) {
	t.Parallel()
	deps := testDeps(t)
	r := New(deps)
	m := execOK(t, r, "node.health", nil)
	if m["status"] != "ok" {
		t.Errorf("status = %v", m["status"])
	}
}

// TestRace_Parallel_Screenshot tests parallel screenshot access.
func TestRace_Parallel_Screenshot(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("screenshot parallel: only run on Windows")
	}
	deps := testDeps(t)
	r := New(deps)
	_, err := r.Execute(req("desktop.screenshot", nil))
	if err != nil {
		t.Logf("screenshot parallel: %v", err)
	}
}

// ─── Stress: Repeated Calls ───────────────────────────────────────────────

// TestRace_RepeatedCalls_NoPanic verifies no panic on repeated calls.
func TestRace_RepeatedCalls_NoPanic(t *testing.T) {
	deps := testDeps(t)
	r := New(deps)

	// Call each capability 20 times sequentially in a single goroutine
	// to verify basic stability
	calls := []struct {
		capability string
		payload    interface{}
	}{
		{"desktop.clipboard.get", nil},
	}

	for i := 0; i < 20; i++ {
		_, err := r.Execute(req(calls[0].capability, calls[0].payload))
		if err != nil {
			// Only log if it's the first failure to avoid spam
			if i == 0 {
				t.Logf("%s call %d: %v", calls[0].capability, i, err)
			}
		}
	}
}

// TestRace_DispatchConcurrent tests concurrent Dispatch calls.
func TestRace_DispatchConcurrent(t *testing.T) {
	d, _, _ := testDepsWithDispatcher(t)

	var wg sync.WaitGroup
	panicCh := make(chan string, 100)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicCh <- fmt.Sprintf("dispatch panic %d: %v", idx, r)
				}
			}()
			_ = d.Dispatch(makeDesktopReq("desktop.clipboard.get", nil))
		}(i)
	}
	wg.Wait()
	close(panicCh)

	for msg := range panicCh {
		t.Error(msg)
	}
}
