package executor

import (
	"fmt"

	"github.com/user/sessionnode/go-core/pkg/types"
)

type streamSubscribePayload struct {
	SessionID string `json:"sessionId"`
	Stream    string `json:"stream"` // "stdout", "stderr", "stdin"
}

func streamSubscribe(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p streamSubscribePayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.SessionID == "" {
		return nil, fmt.Errorf("sessionId is required")
	}
	sess := deps.Sessions.Get(types.SessionID(p.SessionID))
	if sess == nil {
		return nil, fmt.Errorf("session not found: %s", p.SessionID)
	}
	stream, ok := sess.Streams[p.Stream]
	if !ok {
		return nil, fmt.Errorf("unknown stream type: %s", p.Stream)
	}
	return map[string]interface{}{
		"sessionId": p.SessionID,
		"stream":    p.Stream,
		"data":      string(stream.Read()),
	}, nil
}

type streamWritePayload struct {
	SessionID string `json:"sessionId"`
	Stream    string `json:"stream"`
	Data      string `json:"data"`
}

func streamWrite(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p streamWritePayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.SessionID == "" {
		return nil, fmt.Errorf("sessionId is required")
	}

	// Route stdin writes to the process's stdin pipe if a process exists.
	if p.Stream == "stdin" {
		proc := deps.Processes.Get(types.SessionID(p.SessionID))
		if proc != nil && proc.State == "running" {
			if err := deps.Processes.WriteStdin(types.SessionID(p.SessionID), p.Data); err != nil {
				return nil, fmt.Errorf("stdin write error: %w", err)
			}
			return map[string]interface{}{
				"sessionId": p.SessionID,
				"stream":    p.Stream,
				"written":   len(p.Data),
			}, nil
		}
		// No running process: write to session store buffer as fallback.
	}

	sess := deps.Sessions.Get(types.SessionID(p.SessionID))
	if sess == nil {
		return nil, fmt.Errorf("session not found: %s", p.SessionID)
	}
	stream, ok := sess.Streams[p.Stream]
	if !ok {
		return nil, fmt.Errorf("unknown stream type: %s", p.Stream)
	}
	stream.Write([]byte(p.Data))

	// Record into history for replay
	if deps.History != nil {
		deps.History.Record(types.SessionID(p.SessionID), p.Stream, 0, p.Data)
	}

	return map[string]interface{}{
		"sessionId": p.SessionID,
		"stream":    p.Stream,
		"written":   len(p.Data),
	}, nil
}

type streamListPayload struct {
	SessionID string `json:"sessionId"`
}

func streamList(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p streamListPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.SessionID == "" {
		return nil, fmt.Errorf("sessionId is required")
	}
	sess := deps.Sessions.Get(types.SessionID(p.SessionID))
	if sess == nil {
		return nil, fmt.Errorf("session not found: %s", p.SessionID)
	}
	streams := make([]map[string]interface{}, 0)
	for name, s := range sess.Streams {
		streams = append(streams, map[string]interface{}{
			"type": name,
			"size": len(s.Buffer),
		})
	}
	return map[string]interface{}{
		"sessionId": p.SessionID,
		"streams":   streams,
	}, nil
}
