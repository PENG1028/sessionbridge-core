//go:build windows

package process

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ErrAccessDenied is returned by killProcessTree and signalByPID when
// taskkill fails with "Access is denied". Callers (especially tests)
// should treat this as a partial-failure signal: the OS tree could not
// be terminated, but the manager's fallback (direct proc.Kill()) may
// still succeed for the parent process.
var ErrAccessDenied = errors.New("taskkill access denied")

// killProcessTree on Windows uses taskkill /T /F to terminate the entire
// OS process tree in a single call. It does NOT rely on wmic enumeration.
//
// This is the most reliable approach on Windows because:
//   - taskkill /T natively handles the tree in kernel mode
//   - wmic may be unavailable, slow, or miss intermediate processes
//   - There is no Windows equivalent of sending a signal to a single PID
//     without also affecting its children (signals don't exist as a concept)
//
// If the process is already gone (taskkill reports "not found"), we treat
// that as success.
func killProcessTree(pid int, signal string) error {
	// Always use /F (force) — without it taskkill can only terminate GUI
	// processes that respond to WM_CLOSE, not console applications.
	cmd := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")
	out, err := cmd.CombinedOutput()
	if err != nil {
		outStr := string(out)
		// "not found" — process already exited. Treated as success.
		if strings.Contains(outStr, "not found") {
			return nil
		}
		// "Access is denied" — taskkill lacks permissions. Wrap with
		// sentinel so callers (including tests) can detect partial failure.
		if strings.Contains(outStr, "Access is denied") || strings.Contains(outStr, "denied") {
			return fmt.Errorf("taskkill /T /F %d: %w\noutput: %s", pid, ErrAccessDenied, outStr)
		}
		return fmt.Errorf("taskkill /T /F %d: %w\noutput: %s", pid, err, outStr)
	}
	return nil
}

// childrenOf returns direct child PIDs using wmic.
//
// BEST-EFFORT PARTIAL — wmic has known limitations:
//   - Output is UTF-16LE; null bytes are stripped
//   - May miss short-lived or kernel-protected children
//   - Not available on all Windows editions (e.g., Server Core without WMI)
//   - Returns empty slice (not an error) on any failure
//
// Callers MUST NOT rely on childrenOf for correctness — an empty result
// does NOT mean the process has no children, only that enumeration failed
// or returned nothing.
func childrenOf(pid int) ([]int, error) {
	cmd := exec.Command("cmd", "/c",
		fmt.Sprintf("wmic process where (ParentProcessId=%d) get ProcessId /format:value", pid))
	out, err := cmd.Output()
	if err != nil {
		return nil, nil
	}

	clean := strings.Map(func(r rune) rune {
		if r == 0 {
			return -1
		}
		return r
	}, string(out))

	var pids []int
	for _, line := range strings.Split(clean, "\r\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "No Instance(s) Available." {
			continue
		}
		if strings.HasPrefix(line, "ProcessId=") {
			val := strings.TrimPrefix(line, "ProcessId=")
			val = strings.TrimSpace(val)
			if p, err := strconv.Atoi(val); err == nil && p > 0 && p != pid {
				pids = append(pids, p)
			}
		}
	}
	return pids, nil
}

// descendantsOf returns all descendant PIDs via breadth-first wmic traversal.
//
// Inherits all limitations of childrenOf. Returns empty slice on any
// enumeration failure.
func descendantsOf(pid int) ([]int, error) {
	var result []int
	visited := map[int]bool{pid: true}
	queue := []int{pid}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		children, err := childrenOf(current)
		if err != nil {
			return result, err
		}
		for _, c := range children {
			if !visited[c] {
				visited[c] = true
				result = append(result, c)
				queue = append(queue, c)
			}
		}
	}
	return result, nil
}

// signalByPID sends a signal to a process by PID on Windows.
//
// Windows has no POSIX signal concept. All signals use taskkill /T /F
// which terminates the entire process tree. This is a platform limitation:
// Windows cannot signal a parent without also signaling its children.
//
// If the process is already gone, treated as success.
func signalByPID(pid int, signal string) error {
	cmd := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")
	out, err := cmd.CombinedOutput()
	if err != nil {
		outStr := string(out)
		if strings.Contains(outStr, "not found") {
			return nil
		}
		if strings.Contains(outStr, "Access is denied") || strings.Contains(outStr, "denied") {
			return fmt.Errorf("taskkill /T /F %d: %w\noutput: %s", pid, ErrAccessDenied, outStr)
		}
		return fmt.Errorf("taskkill /T /F %d: %w\noutput: %s", pid, err, outStr)
	}
	return nil
}
