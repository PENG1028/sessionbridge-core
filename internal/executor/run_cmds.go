package executor

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/user/sessionnode/go-core/internal/history"
	"github.com/user/sessionnode/go-core/internal/process"
	"github.com/user/sessionnode/go-core/internal/run"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// ── Shared spawn helper ────────────────────────────────────────────────

// spawnRequest holds the common fields for spawning a process.
type spawnRequest struct {
	Command         string   `json:"command"`
	Args            []string `json:"args"`
	Cwd             string   `json:"cwd"`
	Plugin          string   `json:"pluginId"`
	Pty             bool     `json:"pty,omitempty"`
	Cols            int      `json:"cols,omitempty"`
	Rows            int      `json:"rows,omitempty"`
	Kind            string   `json:"kind,omitempty"`
	ParentSessionID string   `json:"parentSessionId,omitempty"`
}

// osc7Prompt returns shell arguments that set up an OSC 7-emitting prompt,
// or nil if the shell type doesn't need / doesn't support prompt setup.
// When args are returned the frontend MUST NOT inject its own prompt via stdin —
// the shell will start with OSC 7 already configured and the terminal stays clean.
func osc7Prompt(command string) []string {
	base := filepath.Base(command)
	switch base {
	case "pwsh.exe", "pwsh", "powershell.exe", "powershell":
		// Pure-string prompt — one output channel, no $host.ui.Write tearing.
		// $([char]27) is unambiguously ESC regardless of backtick parsing rules.
		return []string{
			"-NoExit", "-NoLogo", "-Command",
			`function prompt { $e=[char]27; $p=$PWD.Path.Replace('\','/'); "$e]7;file://$env:COMPUTERNAME/$p$e\PS $PWD> " }`,
		}
	case "bash":
		return []string{"-c", `export PROMPT_COMMAND='printf "\033]7;file://$HOSTNAME$PWD\033\\"'; exec bash`}
	case "cmd.exe", "cmd":
		// cmd.exe: PROMPT env var supports $E for ESC, $P for drive+path, $G for >.
		// Passed via /K so the shell stays interactive after setting the prompt.
		return []string{"/K", `prompt $E]7;file://%COMPUTERNAME%/$P$E\$P$G`}
	default:
		return nil
	}
}

// spawnManagedProcess runs the shared process-spawn logic and returns the
// resulting session ID. Used by both process.spawn and run.create.
func spawnManagedProcess(p spawnRequest, req *types.CapabilityRequest, deps *Deps) (types.SessionID, error) {
	if p.Command == "" {
		if runtime.GOOS == "windows" {
			p.Command = defaultWindowsShell()
		} else {
			p.Command = "bash"
		}
	}

	// Inject OSC 7 prompt args so the shell starts with CWD tracking enabled.
	// This replaces the old stdin-based prompt injection — no echoed function
	// definition, no ">>" continuation prompts, clean terminal from the start.
	if p.Pty && len(p.Args) == 0 {
		if osc7Args := osc7Prompt(p.Command); osc7Args != nil {
			p.Args = osc7Args
		}
	}

	pluginID := req.PluginID
	if p.Plugin != "" {
		pluginID = types.PluginID(p.Plugin)
	}

	cfg := &process.SpawnConfig{
		PluginID: pluginID,
		Kind:     p.Kind,
	}
	if p.ParentSessionID != "" {
		cfg.ParentSessionID = types.SessionID(p.ParentSessionID)
	}

	var sid types.SessionID
	var err error
	if p.Pty {
		sid, err = deps.Processes.SpawnPTY(p.Command, p.Args, p.Cwd, p.Cols, p.Rows, cfg)
	} else {
		sid, err = deps.Processes.Spawn(p.Command, p.Args, p.Cwd, cfg)
	}
	if err != nil {
		return "", fmt.Errorf("spawn failed: %w", err)
	}

	if req.ConnID != "" && deps.ConnRoutes != nil {
		deps.ConnRoutes.Subscribe(req.ConnID, sid, []string{"stdout", "stderr"}, req.PluginID, req.Actor, 0)
	}

	if deps.History != nil {
		hp := types.DefaultHistoryPolicy()
		if err := deps.History.InitSession(sid, hp); err != nil {
			return "", fmt.Errorf("history init: %w", err)
		}
	}

	return sid, nil
}

// ── run.create ──────────────────────────────────────────────────────────

type runCreatePayload struct {
	Kind     string            `json:"kind"`
	Label    string            `json:"label"`
	PluginID string            `json:"pluginId"`
	Command  string            `json:"command"`
	Args     []string          `json:"args"`
	Cwd      string            `json:"cwd"`
	Pty      bool              `json:"pty,omitempty"`
	Cols     int               `json:"cols,omitempty"`
	Rows     int               `json:"rows,omitempty"`
	Policy   *run.Policy       `json:"policy,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func runCreate(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p runCreatePayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	// Validate policy
	rp := run.DefaultPolicy()
	if p.Policy != nil {
		if msg := run.ValidatePolicy(*p.Policy); msg != "" {
			return nil, fmt.Errorf("invalid policy: %s", msg)
		}
		rp = *p.Policy
	}

	// Default kind
	kind := p.Kind
	if kind == "" {
		kind = run.KindTerminal
	}

	// Use pluginId from payload, fallback to request-level
	pluginID := req.PluginID
	if p.PluginID != "" {
		pluginID = types.PluginID(p.PluginID)
	}

	// Spawn the process using shared helper
	sid, err := spawnManagedProcess(spawnRequest{
		Command: p.Command,
		Args:    p.Args,
		Cwd:     p.Cwd,
		Plugin:  p.PluginID,
		Pty:     p.Pty,
		Cols:    p.Cols,
		Rows:    p.Rows,
		Kind:    kind,
	}, req, deps)
	if err != nil {
		return nil, err
	}

	// Create run record
	r := &run.Run{
		NodeID:    "", // filled by caller if needed
		Kind:      kind,
		Label:     p.Label,
		PluginID:  pluginID,
		State:     run.StateRunning,
		SessionID: sid,
		ProcessID: sid,
		Policy:    rp,
		Metadata:  p.Metadata,
	}
	if r.Metadata == nil {
		r.Metadata = make(map[string]string)
	}

	if deps.RunStore != nil {
		r = deps.RunStore.Create(r)
	}

	// Cross-reference: set RunID on the Process so process.list
	// can return run metadata alongside process info.
	if deps.Processes != nil {
		deps.Processes.SetRunID(sid, r.RunID)
	}

	ptyMode := "pipe"
	if p.Pty && deps.Processes != nil {
		if proc := deps.Processes.Get(sid); proc != nil {
			ptyMode = proc.PtyMode()
		} else {
			ptyMode = "pty"
		}
	}

	return map[string]interface{}{
		"runId":     r.RunID,
		"sessionId": string(sid),
		"processId": string(sid),
		"state":     r.State,
		"policy":    r.Policy,
		"ptyMode":   ptyMode,
	}, nil
}

// ── run.list ────────────────────────────────────────────────────────────

type runListPayload struct {
	Kind     string `json:"kind,omitempty"`
	PluginID string `json:"pluginId,omitempty"`
	State    string `json:"state,omitempty"`
}

func runList(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p runListPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		// If payload is empty or unparseable, list all
		p = runListPayload{}
	}

	if deps.RunStore == nil {
		return map[string]interface{}{"runs": []interface{}{}}, nil
	}

	runs := deps.RunStore.List(p.Kind, p.PluginID, p.State)

	// Sync state from ProcessManager for each run that has a process.
	for _, r := range runs {
		if r.ProcessID == "" || deps.Processes == nil {
			continue
		}
		proc := deps.Processes.Get(r.ProcessID)
		if proc == nil {
			// Process gone — classify as orphaned or restorable
			if r.State == run.StateRunning {
				if r.Policy.RestartRestore {
					deps.RunStore.UpdateState(r.RunID, run.StateRestorable)
				} else {
					deps.RunStore.UpdateState(r.RunID, run.StateOrphaned)
				}
			}
			continue
		}
		switch proc.State {
		case "exited":
			if r.State != run.StateExited && r.State != run.StateStopped && r.State != run.StateFailed {
				ns := run.StateExited
				if r.Policy.RestartRestore {
					ns = run.StateRestorable
				}
				deps.RunStore.UpdateState(r.RunID, ns)
			}
		case "running":
			// Keep as-is
		}
	}

	// Re-list after state sync
	runs = deps.RunStore.List(p.Kind, p.PluginID, p.State)

	out := make([]map[string]interface{}, 0, len(runs))
	for _, r := range runs {
		out = append(out, runToMap(r))
	}
	return map[string]interface{}{"runs": out}, nil
}

// ── run.info ────────────────────────────────────────────────────────────

type runInfoPayload struct {
	RunID string `json:"runId"`
}

func runInfo(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p runInfoPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.RunID == "" {
		return nil, fmt.Errorf("runId is required")
	}

	if deps.RunStore == nil {
		return nil, fmt.Errorf("run not found: %s", p.RunID)
	}

	r := deps.RunStore.Get(p.RunID)
	if r == nil {
		return nil, fmt.Errorf("run not found: %s", p.RunID)
	}

	result := runToMap(r)

	// Attach process snapshot if available
	if r.ProcessID != "" && deps.Processes != nil {
		proc := deps.Processes.Get(r.ProcessID)
		if proc != nil {
			cmdPath := ""
			if proc.Cmd != nil {
				cmdPath = proc.Cmd.Path
			}
			result["process"] = map[string]interface{}{
				"sessionId": string(proc.SessionID),
				"pid":       proc.PID,
				"state":     proc.State,
				"exitCode":  proc.ExitCode,
				"command":   cmdPath,
				"createdAt": proc.CreatedAt,
			}
			// Sync run state
			if proc.State == "exited" && r.State == run.StateRunning {
				ns := run.StateExited
				if r.Policy.RestartRestore {
					ns = run.StateRestorable
				}
				deps.RunStore.UpdateState(r.RunID, ns)
				result["state"] = ns
			}
		} else {
			// Process gone — classify
			if r.State == run.StateRunning {
				ns := run.StateOrphaned
				if r.Policy.RestartRestore {
					ns = run.StateRestorable
				}
				deps.RunStore.UpdateState(r.RunID, ns)
				result["state"] = ns
			}
		}
	} else if r.State == run.StateRunning && deps.Processes != nil {
		// No process ref but state is running — orphaned
		ns := run.StateOrphaned
		if r.Policy.RestartRestore {
			ns = run.StateRestorable
		}
		deps.RunStore.UpdateState(r.RunID, ns)
		result["state"] = ns
	}

	return result, nil
}

// ── run.stop ────────────────────────────────────────────────────────────

type runStopPayload struct {
	RunID  string `json:"runId"`
	Signal string `json:"signal"`
	Tree   bool   `json:"tree,omitempty"`
}

func runStop(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p runStopPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.RunID == "" {
		return nil, fmt.Errorf("runId is required")
	}
	if p.Signal == "" {
		p.Signal = "SIGTERM"
	}

	if deps.RunStore == nil {
		return nil, fmt.Errorf("run not found: %s", p.RunID)
	}

	r := deps.RunStore.GetRef(p.RunID)
	if r == nil {
		return nil, fmt.Errorf("run not found: %s", p.RunID)
	}

	if r.State == run.StateOrphaned || r.State == run.StateRestorable {
		// No live process; transition directly to stopped.
		deps.RunStore.UpdateState(p.RunID, run.StateStopped)
		return map[string]interface{}{
			"runId":     p.RunID,
			"state":     run.StateStopped,
			"sessionId": string(r.SessionID),
		}, nil
	}

	if r.State != run.StateRunning {
		return nil, fmt.Errorf("run %s is not running (state: %s)", p.RunID, r.State)
	}

	if err := deps.Processes.Signal(r.ProcessID, p.Signal, p.Tree); err != nil {
		return nil, fmt.Errorf("signal error: %w", err)
	}

	deps.RunStore.UpdateState(p.RunID, run.StateStopped)

	return map[string]interface{}{
		"runId":     p.RunID,
		"state":     run.StateStopped,
		"sessionId": string(r.SessionID),
	}, nil
}

// ── run.updatePolicy ────────────────────────────────────────────────────

type runUpdatePolicyPayload struct {
	RunID  string     `json:"runId"`
	Policy run.Policy `json:"policy"`
}

func runUpdatePolicy(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p runUpdatePolicyPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.RunID == "" {
		return nil, fmt.Errorf("runId is required")
	}

	if deps.RunStore == nil {
		return nil, fmt.Errorf("run not found: %s", p.RunID)
	}

	if err := deps.RunStore.UpdatePolicy(p.RunID, p.Policy); err != nil {
		return nil, fmt.Errorf("policy update failed: %w", err)
	}

	r := deps.RunStore.Get(p.RunID)
	if r == nil {
		return nil, fmt.Errorf("run not found: %s", p.RunID)
	}

	return runToMap(r), nil
}

// ── run.attach ───────────────────────────────────────────────────────────

type runAttachPayload struct {
	RunID       string   `json:"runId"`
	StreamTypes []string `json:"streamTypes,omitempty"`
	Replay      *bool    `json:"replay,omitempty"`
	FromSeq     int64    `json:"fromSeq,omitempty"`
}

func runAttach(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p runAttachPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.RunID == "" {
		return nil, fmt.Errorf("runId is required")
	}

	// Default stream types
	if len(p.StreamTypes) == 0 {
		p.StreamTypes = []string{"stdout", "stderr"}
	}

	// Default replay to true
	replay := true
	if p.Replay != nil {
		replay = *p.Replay
	}

	if deps.RunStore == nil {
		return nil, fmt.Errorf("run not found: %s", p.RunID)
	}

	r := deps.RunStore.Get(p.RunID)
	if r == nil {
		return nil, fmt.Errorf("run not found: %s", p.RunID)
	}

	// Build result from run record
	result := runToMap(r)

	// Attach process snapshot if available
	if r.ProcessID != "" && deps.Processes != nil {
		proc := deps.Processes.Get(r.ProcessID)
		if proc != nil {
			cmdPath := ""
			if proc.Cmd != nil {
				cmdPath = proc.Cmd.Path
			}
			result["process"] = map[string]interface{}{
				"sessionId": string(proc.SessionID),
				"pid":       proc.PID,
				"state":     proc.State,
				"exitCode":  proc.ExitCode,
				"command":   cmdPath,
				"createdAt": proc.CreatedAt,
			}
			// Sync run state if process has exited
			if proc.State == "exited" && r.State == run.StateRunning {
				ns := run.StateExited
				if r.Policy.RestartRestore {
					ns = run.StateRestorable
				}
				deps.RunStore.UpdateState(r.RunID, ns)
				result["state"] = ns
			}
		} else {
			// Process gone — classify
			if r.State == run.StateRunning {
				ns := run.StateOrphaned
				if r.Policy.RestartRestore {
					ns = run.StateRestorable
				}
				deps.RunStore.UpdateState(r.RunID, ns)
				result["state"] = ns
			}
		}
	} else if r.State == run.StateRunning && deps.Processes != nil {
		// No process ref but state is running — classify
		ns := run.StateOrphaned
		if r.Policy.RestartRestore {
			ns = run.StateRestorable
		}
		deps.RunStore.UpdateState(r.RunID, ns)
		result["state"] = ns
	}

	// Build stream subscription info
	// Option A: run.attach returns metadata only; caller uses stream.subscribe separately.
	runState := result["state"]
	isRunning := runState == run.StateRunning
	subs := make([]map[string]interface{}, len(p.StreamTypes))
	for i, st := range p.StreamTypes {
		if isRunning {
			subs[i] = map[string]interface{}{
				"streamType": st,
				"subscribed": false,
				"reason":     "call stream.subscribe after attach",
			}
		} else {
			reason := fmt.Sprintf("run is %s", runState)
			switch runState {
			case run.StateOrphaned:
				reason = "run is orphaned — process no longer exists; re-create run to resume"
			case run.StateRestorable:
				reason = "run is restorable — policy allows restore; re-create run to resume"
			case run.StateExited:
				reason = "run has exited"
			case run.StateStopped:
				reason = "run has been stopped"
			case run.StateFailed:
				reason = "run has failed"
			}
			subs[i] = map[string]interface{}{
				"streamType": st,
				"subscribed": false,
				"reason":     reason,
			}
		}
	}
	result["streamSubscriptions"] = subs

	// Replay history if requested
	if replay && deps.History != nil {
		replayData := make(map[string][]map[string]interface{})
		for _, st := range p.StreamTypes {
			events, err := deps.History.Replay(r.SessionID, st, types.EventSeq(p.FromSeq))
			if err != nil {
				if history.IsHistoryDisabled(err) {
					replayData[st] = []map[string]interface{}{}
					continue
				}
				replayData[st] = []map[string]interface{}{}
				continue
			}
			out := make([]map[string]interface{}, len(events))
			for j, evt := range events {
				out[j] = map[string]interface{}{
					"seq":        evt.EventSeq,
					"data":       evt.Data,
					"streamType": evt.Stream,
					"timestamp":  evt.Timestamp,
				}
			}
			replayData[st] = out
		}
		result["replay"] = replayData
	}

	return result, nil
}

// ── helpers ─────────────────────────────────────────────────────────────

func runToMap(r *run.Run) map[string]interface{} {
	meta := r.Metadata
	if meta == nil {
		meta = make(map[string]string)
	}
	return map[string]interface{}{
		"runId":     r.RunID,
		"nodeId":    r.NodeID,
		"kind":      r.Kind,
		"label":     r.Label,
		"pluginId":  string(r.PluginID),
		"state":     r.State,
		"sessionId": string(r.SessionID),
		"processId": string(r.ProcessID),
		"createdAt": r.CreatedAt,
		"updatedAt": r.UpdatedAt,
		"policy":    r.Policy,
		"metadata":  meta,
	}
}

func defaultWindowsShell() string {
	for _, shell := range []string{"pwsh.exe", "pwsh", "powershell.exe", "powershell", "cmd.exe"} {
		if _, err := exec.LookPath(shell); err == nil {
			return shell
		}
	}
	return "cmd.exe"
}
