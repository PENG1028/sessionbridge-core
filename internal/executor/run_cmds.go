package executor

import (
	"fmt"
	"time"

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

// spawnManagedProcess runs the shared process-spawn logic and returns the
// resulting session ID. Used by both process.spawn and run.create.
func spawnManagedProcess(p spawnRequest, req *types.CapabilityRequest, deps *Deps) (types.SessionID, error) {
	if p.Command == "" {
		return "", fmt.Errorf("command is required")
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

	return map[string]interface{}{
		"runId":     r.RunID,
		"sessionId": string(sid),
		"processId": string(sid),
		"state":     r.State,
		"policy":    r.Policy,
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
			continue
		}
		switch proc.State {
		case "exited":
			if r.State != run.StateExited && r.State != run.StateStopped && r.State != run.StateFailed {
				deps.RunStore.UpdateState(r.RunID, run.StateExited)
				r.State = run.StateExited
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
				deps.RunStore.UpdateState(r.RunID, run.StateExited)
				result["state"] = run.StateExited
			}
		}
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

// Ensure time is used (for spawnManagedProcess future use)
var _ = time.Now
