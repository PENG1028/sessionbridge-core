//go:build !windows

package process

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/creack/pty"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// unixPTYDriver implements PTYDriver for Unix platforms (Linux/macOS)
// using the creack/pty library which opens /dev/ptmx (Unix98 PTY).
type unixPTYDriver struct {
	master *os.File
}

func (d *unixPTYDriver) Write(data string) error {
	_, err := io.WriteString(d.master, data)
	return err
}

func (d *unixPTYDriver) Resize(cols, rows int) error {
	ws := &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)}
	return pty.Setsize(d.master, ws)
}

func (d *unixPTYDriver) Close() error {
	return d.master.Close()
}

func (d *unixPTYDriver) PtyMode() string { return "unix-pty" }

// SpawnPTY starts a new OS process with a PTY (pseudo-terminal).
// The PTY merges stdout and stderr into a single "stdout" stream and
// supports interactive programs (vim, top, shells, etc.).
func (m *Manager) SpawnPTY(command string, args []string, cwd string, cols, rows int, cfg *SpawnConfig) (types.SessionID, error) {
	cmd := exec.Command(command, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}

	ws := &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)}
	if ws.Rows == 0 {
		ws.Rows = 40
	}
	if ws.Cols == 0 {
		ws.Cols = 80
	}

	// Open PTY pair so we can set raw mode on the slave before the child inherits it.
	master, tty, err := pty.Open()
	if err != nil {
		return "", fmt.Errorf("pty open: %w", err)
	}
	defer tty.Close()

	if err := pty.Setsize(master, ws); err != nil {
		master.Close()
		return "", fmt.Errorf("pty setsize: %w", err)
	}

	// Disable echo on the slave — the terminal emulator handles display.
	// Without this, the shell echoes input back to the stream, causing
	// double characters in the frontend.
	if err := setRaw(tty.Fd()); err != nil {
		master.Close()
		return "", fmt.Errorf("pty set raw: %w", err)
	}

	// Attach command to the slave.
	cmd.Stdin = tty
	cmd.Stdout = tty
	cmd.Stderr = tty
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
	}

	if err := cmd.Start(); err != nil {
		master.Close()
		return "", fmt.Errorf("cmd start: %w", err)
	}

	now := time.Now()
	sid := types.SessionID(fmt.Sprintf("sess_pty_%d_%d", cmd.Process.Pid, now.UnixMilli()))

	// Resolve tree metadata.
	parentSID := types.SessionID("")
	rootSID := types.SessionID("")
	pluginID := types.PluginID("")
	kind := ""
	if cfg != nil {
		parentSID = cfg.ParentSessionID
		pluginID = cfg.PluginID
		kind = cfg.Kind
	}
	if parentSID != "" {
		if parent := m.processes[parentSID]; parent != nil {
			if parent.RootSessionID != "" {
				rootSID = parent.RootSessionID
			} else {
				rootSID = parentSID
			}
		}
	}

	proc := &Process{
		SessionID:       sid,
		ParentSessionID: parentSID,
		RootSessionID:   rootSID,
		PluginID:        pluginID,
		Kind:            kind,
		Cmd:             cmd,
		State:           "running",
		CreatedAt:       now.UnixMilli(),
		PID:             cmd.Process.Pid,
		ptyDriver:       &unixPTYDriver{master: master},
	}

	m.mu.Lock()
	m.processes[sid] = proc
	m.mu.Unlock()

	// Fire onSpawn hook before any output goroutines start.
	if m.onSpawn != nil {
		m.onSpawn(sid)
	}

	m.pushEvent(sid, "started", map[string]interface{}{"pid": proc.PID})

	// Read from PTY master (combines stdout+stderr into one stream).
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer master.Close()

		buf := make([]byte, 32*1024)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				seq := types.EventSeq(m.seq.Add(1))
				m.pusher(sid, "stdout", seq, string(buf[:n]))
			}
			if err != nil {
				if err != io.EOF {
					log.Printf("[process] pty session %s read error: %v", sid, err)
				}
				return
			}
		}
	}()

	// Wait for process exit.
	go func() {
		wg.Wait()

		exitErr := cmd.Wait()
		exitCode := 0
		if exitErr != nil {
			if exit, ok := exitErr.(*exec.ExitError); ok {
				exitCode = exit.ExitCode()
			} else {
				log.Printf("[process] pty session %s wait error: %v", sid, exitErr)
				exitCode = -1
			}
		}

		m.mu.Lock()
		if p, ok := m.processes[sid]; ok {
			p.State = "exited"
			p.ExitCode = exitCode
		}
		m.mu.Unlock()

		m.pushEvent(sid, "exited", map[string]interface{}{"exitCode": exitCode})
	}()

	return sid, nil
}

// setRaw disables echo, canonical mode, and signal processing on a terminal fd.
func setRaw(fd uintptr) error {
	var termios syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, uintptr(unsafe.Pointer(&termios))); errno != 0 {
		return errno
	}
	termios.Iflag &^= syscall.BRKINT | syscall.ICRNL | syscall.INPCK | syscall.ISTRIP | syscall.IXON
	termios.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	termios.Cflag &^= syscall.CSIZE | syscall.PARENB
	termios.Cflag |= syscall.CS8
	termios.Oflag |= syscall.ONLCR
	termios.Cc[syscall.VMIN] = 1
	termios.Cc[syscall.VTIME] = 0
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCSETS, uintptr(unsafe.Pointer(&termios)))
	if errno != 0 {
		return errno
	}
	return nil
}

// terminateByHandle is a no-op on Unix — ConPTY processes don't exist here.
func terminateByHandle(_ uintptr) error {
	return fmt.Errorf("process handle signaling not supported on this platform")
}
