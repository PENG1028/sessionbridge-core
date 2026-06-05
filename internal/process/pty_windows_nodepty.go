//go:build windows

package process

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/user/sessionnode/go-core/pkg/types"
)

type nodePTYDriver struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	mu     sync.Mutex
	closed bool
}

type nodePTYRequest struct {
	Type    string   `json:"type"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Cwd     string   `json:"cwd,omitempty"`
	Cols    int      `json:"cols,omitempty"`
	Rows    int      `json:"rows,omitempty"`
	Data    string   `json:"data,omitempty"`
}

type nodePTYEvent struct {
	Type     string `json:"type"`
	PID      int    `json:"pid,omitempty"`
	Data     string `json:"data,omitempty"`
	ExitCode int    `json:"exitCode,omitempty"`
	Message  string `json:"message,omitempty"`
}

func (d *nodePTYDriver) Write(data string) error {
	return d.send(nodePTYRequest{Type: "write", Data: data})
}

func (d *nodePTYDriver) Resize(cols, rows int) error {
	return d.send(nodePTYRequest{Type: "resize", Cols: cols, Rows: rows})
}

func (d *nodePTYDriver) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	stdin := d.stdin
	cmd := d.cmd
	d.mu.Unlock()

	if stdin != nil {
		_ = json.NewEncoder(stdin).Encode(nodePTYRequest{Type: "kill"})
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	return nil
}

func (d *nodePTYDriver) PtyMode() string { return "node-pty" }

func hiddenWindowSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true}
}

// setHideWindow configures an exec.Cmd to run without a visible console window.
func setHideWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
}

// shouldUseNodePTY returns true for interactive shells that benefit from
// a full terminal (ConPTY). Short-lived helper binaries and plugin tasks
// must not go through node-pty; they use plain Spawn/pipe mode instead.
func shouldUseNodePTY(command string) bool {
	if os.Getenv("SESSIONBRIDGE_FORCE_NODE_PTY") == "1" {
		return true
	}
	name := strings.ToLower(filepath.Base(command))
	switch name {
	case "cmd", "cmd.exe", "powershell", "powershell.exe", "pwsh", "pwsh.exe":
		return true
	default:
		return false
	}
}

func (d *nodePTYDriver) send(req nodePTYRequest) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.stdin == nil {
		return fmt.Errorf("node-pty sidecar is closed")
	}
	if err := json.NewEncoder(d.stdin).Encode(req); err != nil {
		return fmt.Errorf("node-pty sidecar write: %w", err)
	}
	return nil
}

func (m *Manager) spawnWithNodePTY(command string, args []string, cwd string, cols, rows int, cfg *SpawnConfig) (types.SessionID, error) {
	nodePath, sidecarPath, projectRoot, err := resolveNodePTYSidecar()
	if err != nil {
		return "", err
	}

	cmd := exec.Command(nodePath, sidecarPath)
	cmd.Dir = projectRoot
	cmd.Env = os.Environ()
	cmd.SysProcAttr = hiddenWindowSysProcAttr()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("node-pty stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return "", fmt.Errorf("node-pty stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		return "", fmt.Errorf("node-pty stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return "", fmt.Errorf("start node-pty sidecar: %w", err)
	}

	driver := &nodePTYDriver{cmd: cmd, stdin: stdin}
	go logNodePTYStderr(stderr)

	events := make(chan nodePTYEvent, 128)
	readErr := make(chan error, 1)
	go readNodePTYEvents(stdout, events, readErr)

	if cwd == "" {
		cwd = projectRoot
	}
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	if err := driver.send(nodePTYRequest{
		Type:    "spawn",
		Command: command,
		Args:    args,
		Cwd:     cwd,
		Cols:    cols,
		Rows:    rows,
	}); err != nil {
		_ = driver.Close()
		return "", err
	}

	var started nodePTYEvent
	select {
	case ev := <-events:
		if ev.Type == "error" {
			_ = driver.Close()
			return "", fmt.Errorf("node-pty sidecar: %s", ev.Message)
		}
		if ev.Type != "started" {
			_ = driver.Close()
			return "", fmt.Errorf("node-pty sidecar unexpected first event: %s", ev.Type)
		}
		started = ev
	case err := <-readErr:
		_ = driver.Close()
		return "", fmt.Errorf("node-pty sidecar exited before start: %w", err)
	case <-time.After(5 * time.Second):
		_ = driver.Close()
		return "", fmt.Errorf("node-pty sidecar start timeout")
	}

	now := time.Now()
	sid := types.SessionID(fmt.Sprintf("sess_pty_%d_%d", started.PID, now.UnixMilli()))
	parentSID, rootSID, pluginID, kind := m.resolveProcessMetadata(cfg)

	proc := &Process{
		SessionID:       sid,
		ParentSessionID: parentSID,
		RootSessionID:   rootSID,
		PluginID:        pluginID,
		Kind:            kind,
		Cmd:             cmd,
		State:           "running",
		CreatedAt:       now.UnixMilli(),
		PID:             started.PID,
		ptyDriver:       driver,
	}

	m.mu.Lock()
	m.processes[sid] = proc
	m.mu.Unlock()

	if m.onSpawn != nil {
		m.onSpawn(sid)
	}
	m.pushEvent(sid, "started", map[string]interface{}{"pid": proc.PID})

	go m.forwardNodePTYEvents(sid, events, readErr, cmd)

	return sid, nil
}

func (m *Manager) resolveProcessMetadata(cfg *SpawnConfig) (types.SessionID, types.SessionID, types.PluginID, string) {
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
		m.mu.Lock()
		if parent := m.processes[parentSID]; parent != nil {
			if parent.RootSessionID != "" {
				rootSID = parent.RootSessionID
			} else {
				rootSID = parentSID
			}
		}
		m.mu.Unlock()
	}
	return parentSID, rootSID, pluginID, kind
}

func (m *Manager) forwardNodePTYEvents(sid types.SessionID, events <-chan nodePTYEvent, readErr <-chan error, cmd *exec.Cmd) {
	exitCode := 0
	exited := false
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				if cmd != nil {
					_ = cmd.Wait()
				}
				m.finishNodePTY(sid, exitCode)
				return
			}
			switch ev.Type {
			case "stdout":
				if ev.Data != "" {
					seq := types.EventSeq(m.seq.Add(1))
					m.pusher(sid, "stdout", seq, ev.Data)
				}
			case "exit":
				exitCode = ev.ExitCode
				exited = true
			case "error":
				log.Printf("[process] node-pty session %s error: %s", sid, ev.Message)
			}
		case err := <-readErr:
			// Drain pending events before processing the error so
			// we don't lose the exit event sent just before pipe close.
			for {
				select {
				case ev, ok := <-events:
					if !ok {
						goto finishNodePTY
					}
					if ev.Type == "exit" {
						exitCode = ev.ExitCode
						exited = true
					}
				default:
					goto finishNodePTY
				}
			}
		finishNodePTY:
			if err != nil && err != io.EOF && !exited {
				log.Printf("[process] node-pty session %s read error: %v", sid, err)
			}
			if cmd != nil {
				if waitErr := cmd.Wait(); waitErr != nil && !exited {
					if exitErr, ok := waitErr.(*exec.ExitError); ok {
						exitCode = exitErr.ExitCode()
					}
				}
			}
			m.finishNodePTY(sid, exitCode)
			return
		}
	}
}

func (m *Manager) finishNodePTY(sid types.SessionID, exitCode int) {
	m.mu.Lock()
	if p, ok := m.processes[sid]; ok && p.State != "exited" {
		p.State = "exited"
		p.ExitCode = exitCode
	}
	m.mu.Unlock()
	m.pushEvent(sid, "exited", map[string]interface{}{"exitCode": exitCode})
}

func readNodePTYEvents(stdout io.Reader, events chan<- nodePTYEvent, readErr chan<- error) {
	defer close(events)
	decoder := json.NewDecoder(stdout)
	for {
		var ev nodePTYEvent
		if err := decoder.Decode(&ev); err != nil {
			readErr <- err
			return
		}
		events <- ev
	}
}

func logNodePTYStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		log.Printf("[process] node-pty sidecar: %s", scanner.Text())
	}
}

func resolveNodePTYSidecar() (nodePath, sidecarPath, projectRoot string, err error) {
	nodePath, err = exec.LookPath("node")
	if err != nil {
		return "", "", "", fmt.Errorf("node not found in PATH")
	}

	roots := candidateProjectRoots()
	for _, root := range roots {
		sidecar := filepath.Join(root, "scripts", "node-pty-sidecar.js")
		if fileExists(sidecar) && dirExists(filepath.Join(root, "node_modules", "node-pty")) {
			return nodePath, sidecar, root, nil
		}
	}
	return "", "", "", fmt.Errorf("node-pty sidecar or node_modules not found")
}

func candidateProjectRoots() []string {
	seen := map[string]bool{}
	add := func(paths *[]string, p string) {
		if p == "" {
			return
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return
		}
		if !seen[abs] {
			seen[abs] = true
			*paths = append(*paths, abs)
		}
	}

	var roots []string
	if wd, err := os.Getwd(); err == nil {
		addAncestors(&roots, add, wd, 6)
	}
	if exe, err := os.Executable(); err == nil {
		addAncestors(&roots, add, filepath.Dir(exe), 6)
	}
	return roots
}

func addAncestors(roots *[]string, add func(*[]string, string), start string, max int) {
	current := start
	for i := 0; i < max && current != ""; i++ {
		add(roots, current)
		parent := filepath.Dir(current)
		if parent == current {
			return
		}
		current = parent
	}
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
