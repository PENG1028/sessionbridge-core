package process

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
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
	SessionID       types.SessionID
	ParentSessionID types.SessionID // parent process session ID (empty if root)
	RootSessionID   types.SessionID // root of the process tree
	PluginID        types.PluginID  // plugin that owns this process
	Kind            string          // process kind: "terminal", "task", "agent", etc.
	Cmd             *exec.Cmd
	State           string
	ExitCode        int
	CreatedAt       int64
	PID             int
	StdinPipe       io.WriteCloser
	ptyDriver       PTYDriver // non-nil for PTY sessions (SpawnPTY)
	processHandle   uintptr   // OS process handle (used on Windows ConPTY)
}

// PtyMode returns the PTY backend identifier for diagnostics, or "pipe"
// if no PTY driver is attached.
func (p *Process) PtyMode() string {
	if p.ptyDriver != nil {
		return p.ptyDriver.PtyMode()
	}
	return "pipe"
}

// SpawnConfig carries optional metadata for spawning a process.
// Pass nil to Spawn/SpawnPTY for default values.
type SpawnConfig struct {
	PluginID        types.PluginID
	Kind            string
	ParentSessionID types.SessionID
}

// Manager spawns and tracks OS processes, pushing their output via callbacks.
type Manager struct {
	mu        sync.RWMutex
	processes map[types.SessionID]*Process
	pusher    PushFunc
	eventer   EventFunc
	seq       atomic.Int64
	onSpawn   func(types.SessionID) // called before output goroutines start
}

// NewManager creates a ProcessManager.
func NewManager(pusher PushFunc, eventer EventFunc) *Manager {
	return &Manager{
		processes: make(map[types.SessionID]*Process),
		pusher:    pusher,
		eventer:   eventer,
	}
}

// SetOnSpawn registers a callback that fires when a new process is spawned,
// called with the session ID before any output-reading goroutines start.
// This is useful for initializing associated resources (e.g. history store)
// before any output data arrives.
func (m *Manager) SetOnSpawn(fn func(types.SessionID)) {
	m.onSpawn = fn
}

// Spawn starts a new OS process. Returns the session ID and any error.
// stdout and stderr are merged into a single "stdout" stream (same as PTY).
// cfg carries optional metadata (plugin ID, kind, parent); pass nil for defaults.
func (m *Manager) Spawn(command string, args []string, cwd string, cfg *SpawnConfig) (types.SessionID, error) {
	cmd := exec.Command(command, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("stdin pipe: %w", err)
	}

	// Create a single output pipe manually and redirect both stdout and stderr
	// to it. This ensures we close the write end ourselves after Start(),
	// since on Windows the closeAfterStart mechanism doesn't reliably break
	// Read on the read end.
	outReader, outWriter, err := os.Pipe()
	if err != nil {
		return "", fmt.Errorf("output pipe: %w", err)
	}
	cmd.Stdout = outWriter
	cmd.Stderr = outWriter

	if err := cmd.Start(); err != nil {
		outWriter.Close()
		outReader.Close()
		return "", fmt.Errorf("start: %w", err)
	}

	// Close the write end now that the child has inherited it.
	// This ensures the read end will get EOF after the process exits
	// (once the child's inherited handles are also closed).
	outWriter.Close()

	now := time.Now()
	sid := types.SessionID(fmt.Sprintf("sess_proc_%d_%d", cmd.Process.Pid, now.UnixMilli()))

	// Resolve tree metadata.
	parentSID := types.SessionID("")
	pluginID := types.PluginID("")
	kind := ""
	if cfg != nil {
		parentSID = cfg.ParentSessionID
		pluginID = cfg.PluginID
		kind = cfg.Kind
	}
	proc := &Process{
		SessionID:       sid,
		ParentSessionID: parentSID,
		RootSessionID:   "",
		PluginID:        pluginID,
		Kind:            kind,
		Cmd:             cmd,
		State:           "running",
		CreatedAt:       now.UnixMilli(),
		PID:             cmd.Process.Pid,
		StdinPipe:       stdin,
	}

	m.mu.Lock()
	if parentSID != "" {
		if parent := m.processes[parentSID]; parent != nil {
			if parent.RootSessionID != "" {
				proc.RootSessionID = parent.RootSessionID
			} else {
				proc.RootSessionID = parentSID
			}
		}
	}
	m.processes[sid] = proc
	m.mu.Unlock()

	// Fire onSpawn hook before any output goroutines start.
	if m.onSpawn != nil {
		m.onSpawn(sid)
	}

	m.pushEvent(sid, "started", map[string]interface{}{"pid": proc.PID})

	// Single reader for merged stdout+stderr.
	var wg sync.WaitGroup
	wg.Add(1)
	go m.readStream(sid, "stdout", outReader, &wg)

	// Wait for process exit, then ensure the reader completes.
	// On some Windows configurations the pipe read end may not see EOF
	// after the child exits, so we force-close the reader with a timeout.
	go func() {
		// Wait for the process to exit first (this ensures the child's
		// write handles are closed by the OS).
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

		// Now wait for the reader to drain remaining pipe data.
		// If the reader doesn't complete in time (possible on Windows
		// where pipe EOF isn't reliably signaled), force-close the
		// read end to unblock it.
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-done:
			// Reader finished normally.
		case <-time.After(500 * time.Millisecond):
			log.Printf("[process] session %s read timeout, closing reader", sid)
			outReader.Close()
			wg.Wait() // wait for reader to exit after forced close
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
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.processes[sid]
}

// DescendantIDs returns all descendant session IDs for the given process,
// performing a breadth-first traversal of the process tree.
func (m *Manager) DescendantIDs(sid types.SessionID) []types.SessionID {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result []types.SessionID
	queue := []types.SessionID{sid}
	visited := map[types.SessionID]bool{sid: true}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for id, p := range m.processes {
			if p.ParentSessionID == current && !visited[id] {
				visited[id] = true
				result = append(result, id)
				queue = append(queue, id)
			}
		}
	}
	return result
}

// Signal sends a signal to the process. If tree is true, uses OS-level
// process tree termination to signal all descendants and the parent.
// If tree is false, signals only the target process directly.
func (m *Manager) Signal(sid types.SessionID, signal string, tree bool) error {
	m.mu.Lock()
	proc, ok := m.processes[sid]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("process not found: %s", sid)
	}

	if tree {
		// OS-level process tree termination: signals the parent and all
		// descendants in a single call.
		if err := killProcessTree(proc.PID, signal); err != nil {
			// Best-effort: tree kill failed, fall back to direct signal
			// so the target process is at least signaled.
			log.Printf("[process] tree kill failed for %s (pid %d): %v, falling back to direct signal", sid, proc.PID, err)
			return signalProcess(proc, signal)
		}
		return nil
	}

	// tree=false: direct signal only (existing behavior)
	return signalProcess(proc, signal)
}

func signalProcess(proc *Process, signal string) error {
	// ConPTY processes (processHandle != 0) don't use exec.Cmd.
	// Fall back to raw handle termination for kill signals.
	switch signal {
	case "kill", "SIGKILL", "terminate", "SIGTERM":
		if proc.processHandle != 0 {
			return terminateByHandle(proc.processHandle)
		}
		if proc.ptyDriver != nil {
			return proc.ptyDriver.Close()
		}
		if proc.Cmd != nil && proc.Cmd.Process != nil {
			return proc.Cmd.Process.Kill()
		}
		return fmt.Errorf("no process handle")
	case "interrupt", "SIGINT":
		if proc.Cmd != nil && proc.Cmd.Process != nil {
			if err := proc.Cmd.Process.Signal(os.Interrupt); err != nil {
				proc.Cmd.Process.Kill()
			}
			return nil
		}
		if proc.processHandle != 0 {
			return terminateByHandle(proc.processHandle)
		}
		return fmt.Errorf("no process handle")
	default:
		return fmt.Errorf("unsupported signal: %s", signal)
	}
}

// Resize changes the terminal window size for the given session.
// Pipe-mode processes silently no-op since resize has no effect.
func (m *Manager) Resize(sid types.SessionID, cols, rows int) error {
	m.mu.Lock()
	proc, ok := m.processes[sid]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("process not found: %s", sid)
	}
	if proc.ptyDriver == nil {
		// Pipe-mode process — resize has no effect, but returning an
		// error here becomes a 502 Bad Gateway through the proxy.
		// Silently succeed instead.
		return nil
	}
	if proc.State != "running" {
		return fmt.Errorf("process %s is not running (state: %s)", sid, proc.State)
	}
	return proc.ptyDriver.Resize(cols, rows)
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

	if proc.ptyDriver != nil {
		return proc.ptyDriver.Write(data)
	}

	if proc.StdinPipe == nil {
		return fmt.Errorf("process %s has no stdin pipe", sid)
	}
	// Pipe-based processes (non-PTY) expect \n as line terminator,
	// but xterm sends \r. Convert to avoid buffered input on Windows.
	writeData := strings.ReplaceAll(data, "\r", "\n")
	_, err := io.WriteString(proc.StdinPipe, writeData)
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
		if proc.ptyDriver != nil {
			proc.ptyDriver.Close()
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
