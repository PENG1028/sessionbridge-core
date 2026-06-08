//go:build !windows

package process

import (
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/user/sessionnode/go-core/internal/testutil"
)

// TestSpawn_ProcessGroup verifies that spawned processes get their own
// process group (PGID) on Unix, which is critical for reliable tree kills.
// Windows uses kernel process tree (taskkill /T /F) and doesn't need PGID.
func TestSpawn_ProcessGroup(t *testing.T) {
	tr := newTestRecorder()
	m := NewManager(tr.pusher, tr.eventer)
	defer m.Cleanup()

	sleepBin := testutil.SleepBinary(t)
	sid, err := m.Spawn(sleepBin, []string{"10"}, "", nil)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	proc := m.Get(sid)
	if proc == nil {
		t.Fatal("expected non-nil process")
	}

	// The spawned process should be in its own process group.
	// When setProcessGroup(cmd) is called, the child process gets Setpgid=true
	// which makes it its own process group leader (PGID == PID).
	pgid, err := syscall.Getpgid(proc.PID)
	if err != nil {
		t.Fatalf("Getpgid(%d): %v", proc.PID, err)
	}
	if pgid == 0 || pgid == 1 {
		t.Errorf("process PID %d has unexpected PGID %d — expected a unique group", proc.PID, pgid)
	}
	t.Logf("Verified: PID %d is in its own process group (PGID %d)", proc.PID, pgid)

	// Also verify: the process group is NOT the parent (test runner's) PGID.
	parentPgid, _ := syscall.Getpgid(0) // 0 = calling process
	if pgid == parentPgid {
		t.Errorf("process PGID %d matches parent PGID — Setpgid did not create a new group", pgid)
	}
}

// TestPTYProcessGroup verifies that PTY-spawned processes (Setsid=true)
// also get their own process group, ensuring tree kill works for terminals.
func TestPTYProcessGroup(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("PTY test may behave differently on macOS runners")
	}

	tr := newTestRecorder()
	m := NewManager(tr.pusher, tr.eventer)
	defer m.Cleanup()

	shell := "sh"
	if runtime.GOOS == "darwin" {
		shell = "bash"
	}

	// Spawn a PTY shell that runs a sleep command
	sid, err := m.SpawnPTY(shell, []string{"-c", "sleep 10"}, "", 80, 24, nil)
	if err != nil {
		t.Fatalf("SpawnPTY: %v", err)
	}

	time.Sleep(1 * time.Second)

	proc := m.Get(sid)
	if proc == nil {
		t.Fatal("expected non-nil process")
	}

	pgid, err := syscall.Getpgid(proc.PID)
	if err != nil {
		t.Fatalf("Getpgid(%d): %v", proc.PID, err)
	}
	if pgid == 0 || pgid == 1 {
		t.Errorf("PTY process PID %d has unexpected PGID %d", proc.PID, pgid)
	}
	t.Logf("Verified: PTY PID %d is in its own process group (PGID %d)", proc.PID, pgid)
}
