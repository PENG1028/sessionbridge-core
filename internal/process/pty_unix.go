//go:build !windows

package process

import (
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// SpawnPTY starts a new OS process with a PTY (pseudo-terminal).
// The PTY merges stdout and stderr into a single "stdout" stream and
// supports interactive programs (vim, top, shells, etc.).
// cfg carries optional metadata; pass nil for defaults.
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

	master, err := pty.StartWithSize(cmd, ws)
	if err != nil {
		return "", fmt.Errorf("pty start: %w", err)
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
		ptyMaster:       master,
		pty:             true,
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

// Resize changes the PTY window size for the given session.
func (m *Manager) Resize(sid types.SessionID, cols, rows int) error {
	m.mu.Lock()
	proc, ok := m.processes[sid]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("process not found: %s", sid)
	}
	if !proc.pty {
		return fmt.Errorf("process %s is not a PTY session", sid)
	}
	if proc.State != "running" {
		return fmt.Errorf("process %s is not running (state: %s)", sid, proc.State)
	}

	ws := &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)}
	if err := pty.Setsize(proc.ptyMaster, ws); err != nil {
		return fmt.Errorf("resize error: %w", err)
	}
	return nil
}
