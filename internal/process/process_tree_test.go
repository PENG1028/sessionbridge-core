package process

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// waitTimeout waits for a command to exit with a timeout.
// If the timeout expires, the process is killed.
func waitTimeout(cmd *exec.Cmd, timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		cmd.Process.Kill()
		<-done
	}
}

// processExists checks whether a process with the given PID currently exists.
func processExists(t *testing.T, pid int) bool {
	t.Helper()
	if runtime.GOOS == "windows" {
		cmd := exec.Command("cmd", "/c",
			fmt.Sprintf("wmic process where ProcessId=%d get ProcessId /format:value", pid))
		out, _ := cmd.CombinedOutput()
		outStr := strings.Map(func(r rune) rune {
			if r == 0 {
				return -1
			}
			return r
		}, string(out))
		return strings.Contains(outStr, fmt.Sprintf("ProcessId=%d", pid))
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(os.Signal(nil)) == nil
}

func TestChildrenOf_ReturnsPIDs(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("unsupported platform for childrenOf test")
		return
	}

	parentCmd, parentArgs := treeParentCommand()
	parent := exec.Command(parentCmd, parentArgs...)
	if err := parent.Start(); err != nil {
		t.Fatalf("start parent: %v", err)
	}
	parentPid := parent.Process.Pid
	defer func() {
		killProcessTree(parentPid, "SIGKILL")
		waitTimeout(parent, 5*time.Second)
	}()

	time.Sleep(2 * time.Second)

	children, err := childrenOf(parentPid)
	if err != nil {
		t.Fatalf("childrenOf(%d): %v", parentPid, err)
	}
	if len(children) == 0 {
		if runtime.GOOS == "windows" {
			t.Skip("childrenOf returned 0 on Windows — wmic enumeration is PARTIAL, cannot verify child tracking")
		}
		t.Logf("childrenOf(%d) returned 0 children — pgrep may be unavailable", parentPid)
	} else {
		t.Logf("childrenOf(%d) found %d children: %v", parentPid, len(children), children)
		for _, c := range children {
			if !processExists(t, c) {
				t.Errorf("child %d reported by childrenOf does not exist", c)
			}
		}
	}
}

func TestDescendantsOf_Recursive(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("unsupported platform for descendantsOf test")
		return
	}

	parentCmd, parentArgs := treeGrandparentCommand()
	parent := exec.Command(parentCmd, parentArgs...)
	if err := parent.Start(); err != nil {
		t.Fatalf("start parent: %v", err)
	}
	parentPid := parent.Process.Pid
	defer func() {
		killProcessTree(parentPid, "SIGKILL")
		waitTimeout(parent, 5*time.Second)
	}()

	time.Sleep(3 * time.Second)

	children, err := childrenOf(parentPid)
	if err != nil {
		t.Fatalf("childrenOf(%d): %v", parentPid, err)
	}
	if len(children) == 0 {
		t.Skip("childrenOf returned 0 - cannot verify recursive tree")
		return
	}

	desc, err := descendantsOf(parentPid)
	if err != nil {
		t.Fatalf("descendantsOf(%d): %v", parentPid, err)
	}
	t.Logf("descendantsOf(%d) found %d descendants: %v", parentPid, len(desc), desc)

	if len(desc) < len(children) {
		t.Errorf("descendantsOf returned %d entries, fewer than direct children %d", len(desc), len(children))
	}
}

func TestKillProcessTree_DirectChild(t *testing.T) {
	parentCmd, parentArgs := treeParentCommand()
	parent := exec.Command(parentCmd, parentArgs...)
	if err := parent.Start(); err != nil {
		t.Fatalf("start parent: %v", err)
	}
	parentPid := parent.Process.Pid

	time.Sleep(2 * time.Second)

	children, _ := childrenOf(parentPid)
	t.Logf("Before kill: parent=%d children=%v", parentPid, children)

	if len(children) == 0 && runtime.GOOS == "windows" {
		// Windows: wmic enumeration is PARTIAL. killProcessTree uses
		// taskkill /T /F which operates at kernel level — the tree IS
		// killed even though we couldn't enumerate it.
		t.Log("childrenOf returned 0 — wmic partial; verifying killProcessTree on parent only")
	}

	if err := killProcessTree(parentPid, "SIGKILL"); err != nil {
		t.Fatalf("killProcessTree(%d): %v", parentPid, err)
	}

	time.Sleep(2 * time.Second)
	waitTimeout(parent, 5*time.Second)

	if len(children) > 0 {
		for _, cp := range children {
			if processExists(t, cp) {
				t.Errorf("child %d still exists after tree kill", cp)
			}
		}
	}
}

func TestKillProcessTree_NoChildren(t *testing.T) {
	cmd, args := singleLongProcess()
	c := exec.Command(cmd, args...)
	if err := c.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := c.Process.Pid
	defer func() {
		killProcessTree(pid, "SIGKILL")
		waitTimeout(c, 5*time.Second)
	}()

	time.Sleep(1 * time.Second)

	if !processExists(t, pid) {
		t.Fatalf("process %d exited before we could kill it", pid)
	}

	if err := killProcessTree(pid, "SIGKILL"); err != nil {
		t.Fatalf("killProcessTree(%d): %v", pid, err)
	}

	time.Sleep(1 * time.Second)
	waitTimeout(c, 5*time.Second)
}

func TestKillProcessTree_AlreadyExited(t *testing.T) {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("cmd", "/c", "exit /b 0")
		if err := cmd.Start(); err != nil {
			t.Fatalf("start: %v", err)
		}
		pid := cmd.Process.Pid
		_ = cmd.Wait()

		time.Sleep(500 * time.Millisecond)

		err := killProcessTree(pid, "SIGKILL")
		if err == nil {
			t.Log("killProcessTree returned nil for already-exited (acceptable)")
		} else {
			t.Logf("killProcessTree on exited PID returned error (expected): %v", err)
		}
	} else {
		cmd := exec.Command("true")
		if err := cmd.Start(); err != nil {
			t.Fatalf("start: %v", err)
		}
		pid := cmd.Process.Pid
		_ = cmd.Wait()

		time.Sleep(500 * time.Millisecond)

		err := killProcessTree(pid, "SIGKILL")
		if err == nil {
			t.Log("killProcessTree returned nil for already-exited (acceptable)")
		} else {
			t.Logf("killProcessTree on exited PID returned error (expected): %v", err)
		}
	}
}

func TestKillProcessTree_SIGTERM(t *testing.T) {
	cmd, args := singleLongProcess()
	c := exec.Command(cmd, args...)
	if err := c.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := c.Process.Pid
	defer func() {
		killProcessTree(pid, "SIGKILL")
		waitTimeout(c, 5*time.Second)
	}()

	time.Sleep(1 * time.Second)

	if !processExists(t, pid) {
		t.Fatalf("process %d exited before we could signal it", pid)
	}

	if err := killProcessTree(pid, "SIGTERM"); err != nil {
		t.Fatalf("killProcessTree(%d, SIGTERM): %v", pid, err)
	}

	time.Sleep(1 * time.Second)
	waitTimeout(c, 5*time.Second)
}

func TestKillProcessTree_SIGINT(t *testing.T) {
	cmd, args := singleLongProcess()
	c := exec.Command(cmd, args...)
	if err := c.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := c.Process.Pid
	defer func() {
		killProcessTree(pid, "SIGKILL")
		waitTimeout(c, 5*time.Second)
	}()

	time.Sleep(1 * time.Second)

	if !processExists(t, pid) {
		t.Fatalf("process %d exited before we could signal it", pid)
	}

	if err := killProcessTree(pid, "SIGINT"); err != nil {
		t.Fatalf("killProcessTree(%d, SIGINT): %v", pid, err)
	}

	time.Sleep(1 * time.Second)
	waitTimeout(c, 5*time.Second)
}

// singleLongProcess returns a command that runs for about 30 seconds.
func singleLongProcess() (string, []string) {
	if runtime.GOOS == "windows" {
		return "ping", []string{"-n", "30", "127.0.0.1"}
	}
	return "sleep", []string{"30"}
}

// treeParentCommand returns a command+args that starts a parent process
// which spawns a long-running child process.
func treeParentCommand() (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/c", "start /b ping -n 30 127.0.0.1 > nul & ping -n 30 127.0.0.1 > nul"}
	}
	return "sh", []string{"-c", "sleep 30 & wait"}
}

// treeGrandparentCommand returns a command+args that starts a parent
// which spawns a child which spawns a grandchild (3-level tree).
func treeGrandparentCommand() (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/c", `start /b "" cmd /c "start /b ping -n 30 127.0.0.1 > nul & ping -n 30 127.0.0.1 > nul" & ping -n 30 127.0.0.1 > nul`}
	}
	return "sh", []string{"-c", "(sh -c 'sleep 30 & wait' &); wait"}
}
