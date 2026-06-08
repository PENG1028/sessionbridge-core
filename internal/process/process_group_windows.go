//go:build windows

package process

import "os/exec"

// setProcessGroup is a no-op on Windows.
//
// Windows does not have the Unix concept of process groups (PGID).
// Instead, process tree tracking is handled at the OS kernel level:
// killProcessTree uses taskkill /T /F which natively terminates the
// entire process tree rooted at the given PID, regardless of any
// intermediate group or session boundaries.
//
// The Windows kernel's process tree (via CreateToolhelp32Snapshot)
// is immune to the "reparented grandchild" problem that affects
// Unix's pgrep-based traversal — taskkill /T finds everything.
func setProcessGroup(cmd *exec.Cmd) {
	// No-op: Windows process tree management is handled by
	// the kernel via CreateProcess → process handle relationships.
	// taskkill /T /F in killProcessTree covers all descendants.
}
