//go:build windows

package process

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// childrenOf returns direct child PIDs using wmic.
// Returns an empty slice (not an error) if wmic is unavailable or returns no matches.
func childrenOf(pid int) ([]int, error) {
	cmd := exec.Command("cmd", "/c",
		fmt.Sprintf("wmic process where (ParentProcessId=%d) get ProcessId /format:value", pid))
	out, err := cmd.Output()
	if err != nil {
		// wmic may not be available; treat as no children.
		return nil, nil
	}
	var pids []int
	for _, line := range strings.Split(string(out), "\r\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "ProcessId=") {
			val := strings.TrimPrefix(line, "ProcessId=")
			val = strings.TrimSpace(val)
			if p, err := strconv.Atoi(val); err == nil && p > 0 {
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

// signalByPID sends a signal to a process by PID on Windows.
//
// Signal mapping:
//   - "kill", "SIGKILL"  -> os.Kill
//   - "interrupt", "SIGINT" -> os.Interrupt (maps to CTRL+BREAK on Windows)
//   - "terminate", "SIGTERM" (default) -> os.Kill
func signalByPID(pid int, signal string) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}

	switch signal {
	case "interrupt", "SIGINT":
		// os.Interrupt on Windows sends os.Kill actually — but try interrupt first.
		if err := p.Signal(os.Interrupt); err != nil {
			return p.Signal(os.Kill)
		}
		return nil
	default:
		// On Windows, SIGTERM/SIGKILL both map to os.Kill
		return p.Signal(os.Kill)
	}
}

// Ensure syscall is referenced (though unused on Windows, kept for compatibility).
var _ = syscall.StringToUTF16
