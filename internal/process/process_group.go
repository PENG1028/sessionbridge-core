//go:build !windows

package process

import (
	"os/exec"
	"syscall"
)

// setProcessGroup configures the command to run in its own process group.
//
// This is critical for reliable process tree cleanup: when the shell or any
// spawned process creates child processes, they inherit the same PGID by
// default. killProcessTree can then SIGTERM/SIGKILL the entire group at once
// by sending to -PGID, covering the common case (bash → claude → python).
//
// Without this, each fork would stay in the parent's process group, making
// targeted tree kill impossible without pgrep-based BFS (which misses
// grandchildren when middle processes have exited).
//
// The Setsid flag in pty_unix.go also creates a new PGID implicitly, but
// this helper is used for non-PTY spawns (Manager.Spawn) to ensure every
// Core-tracked process has its own cleanable process group.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}
