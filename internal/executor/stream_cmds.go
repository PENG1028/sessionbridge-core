package executor

import (
	"fmt"
	"strings"

	"github.com/user/sessionnode/go-core/pkg/types"
)

type streamSubscribePayload struct {
	SessionID  string          `json:"sessionId"`
	Stream     string          `json:"stream"`     // legacy
	StreamType string          `json:"streamType"` // canonical — prefer over "stream"
	FromSeq    types.EventSeq  `json:"fromSeq,omitempty"`
}

func streamSubscribe(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p streamSubscribePayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.SessionID == "" {
		return nil, fmt.Errorf("sessionId is required")
	}
	// Accept both "streamType" (canonical) and "stream" (legacy)
	stream := p.Stream
	if p.StreamType != "" {
		stream = p.StreamType
	}
	sid := types.SessionID(p.SessionID)

	// Verify session or process exists before subscribing
	sess := deps.Sessions.Get(sid)
	proc := deps.Processes.Get(sid)
	if sess == nil && proc == nil {
		return nil, fmt.Errorf("session not found: %s", p.SessionID)
	}

	// Parse stream types (comma-separated, e.g. "stdout,stderr")
	streamTypes := strings.Split(stream, ",")
	for i := range streamTypes {
		streamTypes[i] = strings.TrimSpace(streamTypes[i])
	}

	// Register subscription using the multi-subscriber model.
	sub := deps.ConnRoutes.Subscribe(req.ConnID, sid, streamTypes, req.PluginID, req.Actor, p.FromSeq)

	result := map[string]interface{}{
		"subscriptionId": sub.ID,
		"sessionId":      p.SessionID,
		"stream":         stream,
		"streamType":     stream,
	}

	// Replay history events from fromSeq when specified (reconnect scenario).
	if p.FromSeq > 0 && deps.History != nil {
		for _, st := range streamTypes {
			events, err := deps.History.Replay(sid, st, p.FromSeq)
			if err == nil && len(events) > 0 {
				out := make([]map[string]interface{}, len(events))
				for i, evt := range events {
					out[i] = eventToMap(evt)
				}
				result["events"] = out
				result["replayCount"] = len(out)
				break // return events from the first matching stream
			}
		}
	}

	// Fallback: replay session buffer (for new subscribers without fromSeq).
	if p.FromSeq == 0 && sess != nil {
		for _, st := range streamTypes {
			if s, ok := sess.Streams[st]; ok {
				result["data"] = string(s.Read())
				break
			}
		}
	}

	return result, nil
}

type streamWritePayload struct {
	SessionID  string `json:"sessionId"`
	Stream     string `json:"stream"`     // legacy
	StreamType string `json:"streamType"` // canonical — prefer over "stream"
	Data       string `json:"data"`
}

func streamWrite(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p streamWritePayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.SessionID == "" {
		return nil, fmt.Errorf("sessionId is required")
	}

	// Accept both "streamType" (canonical) and "stream" (legacy)
	stream := p.Stream
	if p.StreamType != "" {
		stream = p.StreamType
	}

	// Route stdin writes to the process's stdin pipe if a process exists.
	if stream == "stdin" {
		proc := deps.Processes.Get(types.SessionID(p.SessionID))
		if proc != nil && proc.State == "running" {
			if err := deps.Processes.WriteStdin(types.SessionID(p.SessionID), p.Data); err != nil {
				return nil, fmt.Errorf("stdin write error: %w", err)
			}
			return map[string]interface{}{
				"sessionId":  p.SessionID,
				"stream":     stream,
				"streamType": stream,
				"written":    len(p.Data),
			}, nil
		}
		// No running process: write to session store buffer as fallback.
	}

	sess := deps.Sessions.Get(types.SessionID(p.SessionID))
	if sess == nil {
		return nil, fmt.Errorf("session not found: %s", p.SessionID)
	}
	sessStream, ok := sess.Streams[stream]
	if !ok {
		return nil, fmt.Errorf("unknown stream type: %s", stream)
	}
	sessStream.Write([]byte(p.Data))

	// Record into history for replay
	if deps.History != nil {
		deps.History.Record(types.SessionID(p.SessionID), stream, 0, p.Data)
	}

	return map[string]interface{}{
		"sessionId":  p.SessionID,
		"stream":     stream,
		"streamType": stream,
		"written":    len(p.Data),
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
