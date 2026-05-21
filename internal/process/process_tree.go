package process

import "fmt"

// killProcessTree attempts to terminate the entire process tree rooted at pid.
//
// Strategy (best-effort):
//  1. Enumerate all descendants (children, grandchildren, etc.)
//  2. Signal descendants first (leaves before root), then the parent
//  3. If enumeration fails, still signal the parent (never leave it running)
//
// Returns an error only if even the parent couldn't be signaled.
func killProcessTree(pid int, signal string) error {
	desc, descErr := descendantsOf(pid)

	// Signal descendants first (best-effort, errors are non-fatal).
	for _, childPid := range desc {
		_ = signalByPID(childPid, signal)
	}

	// Always signal the parent, even if descendant enumeration failed.
	if err := signalByPID(pid, signal); err != nil {
		return fmt.Errorf("failed to signal parent %d: %w", pid, err)
	}

	_ = descErr
	return nil
}
