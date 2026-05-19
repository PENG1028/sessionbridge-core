package process

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/user/sessionnode/go-core/pkg/types"
)

// PushFunc is called by the process manager when a process produces output.
type PushFunc func(sid types.SessionID, streamType string, seq types.EventSeq, data string)

// EventFunc is called when a process lifecycle event occurs (started, exited).
type EventFunc func(sid types.SessionID, seq types.EventSeq, eventType string, data interface{})

// Process represents a running or completed OS process.
type Process struct {
	SessionID types.SessionID
	Cmd       *exec.Cmd
	State     string
	ExitCode  int
	CreatedAt int64
	PID       int
	StdinPipe io.WriteCloser
	ptyMaster *os.File // non-nil for PTY sessions
	pty       bool     // true when spawned via SpawnPTY
}

// Manager spawns and tracks OS processes, pushing their output via callbacks.
type Manager struct {
	mu        sync.Mutex
	processes map[types.SessionID]*Process
	pusher    PushFunc
	eventer   EventFunc
	seq       atomic.Int64
}

// NewManager creates a ProcessManager.
func NewManager(pusher PushFunc, eventer EventFunc) *Manager {
	return &Manager{
		processes: make(map[types.SessionID]*Process),
		pusher:    pusher,
		eventer:   eventer,
	}
}

// Spawn starts a new OS process. Returns the session ID and any error.
func (m *Manager) Spawn(command string, args []string, cwd string) (types.SessionID, error) {
	cmd := exec.Command(command, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start: %w", err)
	}

	now := time.Now()
	sid := types.SessionID(fmt.Sprintf("sess_proc_%d_%d", cmd.Process.Pid, now.UnixMilli()))

	proc := &Process{
		SessionID: sid,
		Cmd:       cmd,
		State:     "running",
		CreatedAt: now.UnixMilli(),
		PID:       cmd.Process.Pid,
		StdinPipe: stdin,
	}

	m.mu.Lock()
	m.processes[sid] = proc
	m.mu.Unlock()

	m.pushEvent(sid, "started", map[string]interface{}{"pid": proc.PID})

	// Track readers completion
	var readersWg sync.WaitGroup
	readersWg.Add(2)
	go m.readStream(sid, "stdout", stdout, &readersWg)
	go m.readStream(sid, "stderr", stderr, &readersWg)

	// Wait for all output to be read, then wait for process exit
	go func() {
		readersWg.Wait()

		exitErr := cmd.Wait()
		exitCode := 0
		if exitErr != nil {
			if exit, ok := exitErr.(*exec.ExitError); ok {
				exitCode = exit.ExitCode()
			} else {
				log.Printf("[process] session %s wait error: %v", sid, exitErr)
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

// Get returns the process for the given session ID, or nil.
func (m *Manager) Get(sid types.SessionID) *Process {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.processes[sid]
}

// Signal sends a signal to the process.
func (m *Manager) Signal(sid types.SessionID, signal string) error {
	m.mu.Lock()
	proc, ok := m.processes[sid]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("process not found: %s", sid)
	}
	if proc.State != "running" {
		return fmt.Errorf("process %s is not running (state: %s)", sid, proc.State)
	}
	switch signal {
	case "kill", "SIGKILL", "terminate", "SIGTERM":
		return proc.Cmd.Process.Kill()
	case "interrupt", "SIGINT":
		if err := proc.Cmd.Process.Signal(os.Interrupt); err != nil {
			proc.Cmd.Process.Kill()
		}
		return nil
	default:
		return fmt.Errorf("unsupported signal: %s", signal)
	}
}

// WriteStdin writes data to the process's stdin (or PTY master).
func (m *Manager) WriteStdin(sid types.SessionID, data string) error {
	m.mu.Lock()
	proc, ok := m.processes[sid]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("process not found: %s", sid)
	}
	if proc.State != "running" {
		return fmt.Errorf("process %s is not running (state: %s)", sid, proc.State)
	}

	if proc.pty {
		_, err := io.WriteString(proc.ptyMaster, data)
		if err != nil {
			return fmt.Errorf("pty write error: %w", err)
		}
		return nil
	}

	if proc.StdinPipe == nil {
		return fmt.Errorf("process %s has no stdin pipe", sid)
	}
	_, err := io.WriteString(proc.StdinPipe, data)
	if err != nil {
		return fmt.Errorf("stdin write error: %w", err)
	}
	return nil
}

// CloseStdin closes the process's stdin pipe, signaling EOF to the process.
func (m *Manager) CloseStdin(sid types.SessionID) error {
	m.mu.Lock()
	proc, ok := m.processes[sid]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("process not found: %s", sid)
	}
	if proc.StdinPipe == nil {
		return nil
	}
	return proc.StdinPipe.Close()
}

// List returns all tracked processes.
func (m *Manager) List() []*Process {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Process, 0, len(m.processes))
	for _, p := range m.processes {
		out = append(out, p)
	}
	return out
}

// Count returns the number of tracked processes.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.processes)
}

// Cleanup kills all running processes and removes them from tracking.
func (m *Manager) Cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for sid, proc := range m.processes {
		if proc.ptyMaster != nil {
			proc.ptyMaster.Close()
		}
		if proc.StdinPipe != nil {
			proc.StdinPipe.Close()
		}
		if proc.State == "running" && proc.Cmd != nil && proc.Cmd.Process != nil {
			proc.Cmd.Process.Kill()
		}
		delete(m.processes, sid)
	}
}

func (m *Manager) readStream(sid types.SessionID, streamType string, reader io.ReadCloser, wg *sync.WaitGroup) {
	defer wg.Done()
	defer reader.Close()

	buf := make([]byte, 32*1024)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			seq := types.EventSeq(m.seq.Add(1))
			m.pusher(sid, streamType, seq, string(buf[:n]))
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("[process] session %s stream %s read error: %v", sid, streamType, err)
			}
			return
		}
	}
}

func (m *Manager) pushEvent(sid types.SessionID, eventType string, data interface{}) {
	seq := types.EventSeq(m.seq.Add(1))
	m.eventer(sid, seq, eventType, data)
}
