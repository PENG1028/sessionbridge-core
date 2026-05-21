//go:build windows

package process

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// childrenOf returns direct child PIDs using wmic.
// Returns an empty slice (not an error) if wmic is unavailable or returns no matches.
//
// Note: wmic outputs UTF-16LE on some systems, which contains null bytes.
// We strip null bytes before parsing.
func childrenOf(pid int) ([]int, error) {
	cmd := exec.Command("cmd", "/c",
		fmt.Sprintf("wmic process where (ParentProcessId=%d) get ProcessId /format:value", pid))
	out, err := cmd.Output()
	if err != nil {
		// wmic may not be available; treat as no children.
		return nil, nil
	}

	// Strip null bytes (wmic may output UTF-16LE).
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

// descendantsOf returns all descendant PIDs of the given PID (children,
// grandchildren, etc.) using breadth-first traversal via childrenOf.
func descendantsOf(pid int) ([]int, error) {
	var result []int
	queue := []int{pid}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		children, err := childrenOf(current)
		if err != nil {
			return result, err
		}
		for _, c := range children {
			result = append(result, c)
			queue = append(queue, c)
		}
	}
	return result, nil
}

// signalByPID sends a signal to a process by PID on Windows using taskkill.
//
// Note: os.FindProcess().Kill() on Windows opens the process without
// PROCESS_TERMINATE rights, so it fails with "Access is denied". We use
// taskkill instead, which works for any process owned by the current user.
//
// On Windows, taskkill without /F can only terminate GUI processes that
// respond to WM_CLOSE. Console applications require /F. Therefore all
// signals use /F (force) on this platform.
//
// If the process is already gone (taskkill reports "not found"), we treat
// that as a success — the kill target was already achieved.
func signalByPID(pid int, signal string) error {
	cmd := exec.Command("cmd", "/c",
		fmt.Sprintf("taskkill /PID %d /F", pid))
	out, err := cmd.CombinedOutput()
	if err != nil {
		outStr := string(out)
		// "not found" means the process already exited — treat as success.
		if strings.Contains(outStr, "not found") {
			return nil
		}
		return fmt.Errorf("taskkill %d: %w\noutput: %s", pid, err, outStr)
	}
	return nil
}
