//go:build linux || darwin

package process

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// childrenOf returns the PIDs of direct children of the given PID using pgrep -P.
// Returns an empty slice (not an error) if pgrep is unavailable or returns no matches.
func childrenOf(pid int) ([]int, error) {
	cmd := exec.Command("pgrep", "-P", strconv.Itoa(pid))
	out, err := cmd.Output()
	if err != nil {
		// pgrep exits with code 1 when no children are found, which is not an error.
		return nil, nil
	}
	var pids []int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if p, err := strconv.Atoi(line); err == nil {
			pids = append(pids, p)
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

// signalByPID sends a signal to a process by PID.
//
// Signal mapping:
//   - "kill", "SIGKILL"  -> SIGKILL
//   - "interrupt", "SIGINT" -> SIGINT
//   - "terminate", "SIGTERM" (default) -> SIGTERM
func signalByPID(pid int, signal string) error {
	sig := syscall.SIGTERM
	switch signal {
	case "kill", "SIGKILL":
		sig = syscall.SIGKILL
	case "interrupt", "SIGINT":
		sig = syscall.SIGINT
	}

	p, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}
	return p.Signal(sig)
}
