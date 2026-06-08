//go:build !windows

package process

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// killProcessTree attempts to terminate the entire process tree rooted at pid.
//
// Strategy (three-phase, best-effort):
//
//  1. Process group kill — send SIGTERM then SIGKILL to the entire PGID.
//     When the process was spawned with Setpgid=true (or Setsid=true for PTY
//     sessions), all descendants inherit the same PGID by default. This is
//     the most reliable and fastest path, covering bash → claude → python.
//
//  2. pgrep BFS fallback — enumerate all descendants via pgrep -P and signal
//     each one individually. Catches processes that created their own process
//     groups (daemon tools, job-control shells) and left the parent's group.
//     NOTE: this may miss grandchildren if intermediate processes have already
//     exited (they get reparented to init and pgrep -P can't find them).
//
//  3. Always signal the parent — never leave the root process running.
//
// Returns an error only if even the parent couldn't be signaled.
func killProcessTree(pid int, signal string) error {
	// ── Phase 1: Process group kill ────────────────────────────
	// This catches the common case where all processes share a PGID.
	pgid, err := syscall.Getpgid(pid)
	if err == nil && pgid > 1 {
		// Send SIGTERM first (graceful shutdown for well-behaved processes),
		// then SIGKILL after a brief delay to catch anything that ignored TERM.
		// Using -PGID sends to every process in the group.
		killSignal := syscall.SIGTERM
		switch signal {
		case "kill", "SIGKILL":
			killSignal = syscall.SIGKILL
		case "interrupt", "SIGINT":
			killSignal = syscall.SIGINT
		}
		_ = syscall.Kill(-pgid, killSignal)

		// If we used SIGTERM/SIGINT, give a brief grace period then force
		// SIGKILL on anything still alive in the group.
		if killSignal != syscall.SIGKILL {
			time.Sleep(50 * time.Millisecond)
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
	}

	// ── Phase 2: pgrep BFS fallback ────────────────────────────
	// Descendants that created their own process groups (e.g. daemons)
	// won't be caught by the PGID kill. Try to enumerate and signal them.
	desc, _ := descendantsOf(pid)
	for _, childPid := range desc {
		_ = signalByPID(childPid, signal)
	}

	// ── Phase 3: Signal parent ─────────────────────────────────
	// Always kill the root, even if it was already killed by PGID phase.
	// If it's already dead, signalByPID returns an error which we ignore.
	if err := signalByPID(pid, signal); err != nil {
		// If parent is already dead (killed by PGID phase), this is expected.
		// Verify by checking if the process still exists.
		if p, findErr := os.FindProcess(pid); findErr == nil {
			if sigErr := p.Signal(os.Signal(nil)); sigErr != nil {
				// Process doesn't exist — already killed, treat as success.
				return nil
			}
		}
		return fmt.Errorf("failed to signal parent %d: %w", pid, err)
	}

	return nil
}
