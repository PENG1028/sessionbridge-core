package executor

import (
	"fmt"

	"github.com/user/sessionnode/go-core/internal/process"
	"github.com/user/sessionnode/go-core/pkg/types"
)

type processSpawnPayload struct {
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

func processSpawn(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p processSpawnPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.Command == "" {
		return nil, fmt.Errorf("command is required")
	}

	// Determine effective plugin ID: payload overrides request-level.
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
		return nil, fmt.Errorf("spawn failed: %w", err)
	}

	// Auto-subscribe the spawning connection to process output streams.
	if req.ConnID != "" && deps.ConnRoutes != nil {
		deps.ConnRoutes.Subscribe(req.ConnID, sid, []string{"stdout", "stderr"}, req.PluginID, req.Actor, 0)
	}

	// Initialize history for spawned process output.
	if deps.History != nil {
		hp := types.DefaultHistoryPolicy()
		if err := deps.History.InitSession(sid, hp); err != nil {
			return nil, fmt.Errorf("history init: %w", err)
		}
	}

	return map[string]interface{}{
		"sessionId":       string(sid),
		"command":         p.Command,
		"args":            p.Args,
		"state":           "running",
		"pty":             p.Pty,
		"pluginId":        string(pluginID),
		"kind":            p.Kind,
		"parentSessionId": p.ParentSessionID,
	}, nil
}

type processSignalPayload struct {
	SessionID string `json:"sessionId"`
	Signal    string `json:"signal"`
	Tree      bool   `json:"tree,omitempty"`
}

func processSignal(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p processSignalPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.SessionID == "" {
		return nil, fmt.Errorf("sessionId is required")
	}

	if err := deps.Processes.Signal(types.SessionID(p.SessionID), p.Signal, p.Tree); err != nil {
		return nil, fmt.Errorf("signal error: %w", err)
	}

	return map[string]interface{}{
		"sessionId": p.SessionID,
		"signal":    p.Signal,
		"tree":      p.Tree,
	}, nil
}

type processResizePayload struct {
	SessionID string `json:"sessionId"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
}

func processResize(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p processResizePayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.SessionID == "" {
		return nil, fmt.Errorf("sessionId is required")
	}
	if p.Cols <= 0 || p.Rows <= 0 {
		return nil, fmt.Errorf("cols and rows must be positive")
	}
	if err := deps.Processes.Resize(types.SessionID(p.SessionID), p.Cols, p.Rows); err != nil {
		return nil, fmt.Errorf("resize error: %w", err)
	}
	return map[string]interface{}{
		"sessionId": p.SessionID,
		"cols":      p.Cols,
		"rows":      p.Rows,
	}, nil
}

type processListPayload struct {
	PluginID  string `json:"pluginId,omitempty"`
	Kind      string `json:"kind,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

func processList(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p processListPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	procs := deps.Processes.List()
	out := make([]map[string]interface{}, 0, len(procs))
	for _, proc := range procs {
		// Apply filters.
		if p.PluginID != "" && string(proc.PluginID) != p.PluginID {
			continue
		}
		if p.Kind != "" && proc.Kind != p.Kind {
			continue
		}
		if p.SessionID != "" && string(proc.SessionID) != p.SessionID {
			continue
		}

		cmdPath := ""
		if proc.Cmd != nil {
			cmdPath = proc.Cmd.Path
		}
		out = append(out, map[string]interface{}{
			"sessionId":       string(proc.SessionID),
			"processId":       string(proc.SessionID),
			"parentSessionId": string(proc.ParentSessionID),
			"rootSessionId":   string(proc.RootSessionID),
			"pluginId":        string(proc.PluginID),
			"kind":            proc.Kind,
			"pid":             proc.PID,
			"state":           proc.State,
			"exitCode":        proc.ExitCode,
			"command":         cmdPath,
			"createdAt":       proc.CreatedAt,
		})
	}
	return map[string]interface{}{
		"processes": out,
		"total":     len(out),
	}, nil
}
