package executor

import (
	"fmt"

	"github.com/user/sessionnode/go-core/pkg/types"
)

type processSpawnPayload struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Cwd     string   `json:"cwd"`
	Plugin  string   `json:"pluginId"`
	Pty     bool     `json:"pty,omitempty"`
	Cols    int      `json:"cols,omitempty"`
	Rows    int      `json:"rows,omitempty"`
}

func processSpawn(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p processSpawnPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.Command == "" {
		return nil, fmt.Errorf("command is required")
	}

	var sid types.SessionID
	var err error

	if p.Pty {
		sid, err = deps.Processes.SpawnPTY(p.Command, p.Args, p.Cwd, p.Cols, p.Rows)
	} else {
		sid, err = deps.Processes.Spawn(p.Command, p.Args, p.Cwd)
	}
	if err != nil {
		return nil, fmt.Errorf("spawn failed: %w", err)
	}

	return map[string]interface{}{
		"sessionId": string(sid),
		"command":   p.Command,
		"args":      p.Args,
		"state":     "running",
		"pty":       p.Pty,
	}, nil
}

type processSignalPayload struct {
	SessionID string `json:"sessionId"`
	Signal    string `json:"signal"`
}

func processSignal(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p processSignalPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.SessionID == "" {
		return nil, fmt.Errorf("sessionId is required")
	}

	if err := deps.Processes.Signal(types.SessionID(p.SessionID), p.Signal); err != nil {
		return nil, fmt.Errorf("signal error: %w", err)
	}

	return map[string]interface{}{
		"sessionId": p.SessionID,
		"signal":    p.Signal,
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

func processList(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	procs := deps.Processes.List()
	out := make([]map[string]interface{}, 0, len(procs))
	for _, p := range procs {
		out = append(out, map[string]interface{}{
			"sessionId": string(p.SessionID),
			"pid":       p.PID,
			"state":     p.State,
			"exitCode":  p.ExitCode,
			"command":   p.Cmd.Path,
			"createdAt": p.CreatedAt,
		})
	}
	return map[string]interface{}{
		"processes": out,
		"total":     len(out),
	}, nil
}
