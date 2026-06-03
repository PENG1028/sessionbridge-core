package executor

import (
	"fmt"
	"time"

	"github.com/user/sessionnode/go-core/internal/history"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// --- session.history.getPolicy ---

func historyGetPolicy(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p struct {
		SessionID string `json:"sessionId"`
	}
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

	return map[string]interface{}{
		"sessionId": string(sess.ID),
		"history":   sess.HistoryPolicy,
	}, nil
}

// --- session.history.stats ---

func historyStats(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p struct {
		SessionID string `json:"sessionId"`
	}
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.SessionID == "" {
		return nil, fmt.Errorf("sessionId is required")
	}

	if deps.History == nil {
		return nil, fmt.Errorf("history store not available")
	}

	stats, err := deps.History.Stats(types.SessionID(p.SessionID))
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"sessionId":    string(stats.SessionID),
		"mode":         stats.Mode,
		"eventCount":   stats.EventCount,
		"bytesStored":  stats.BytesStored,
		"bytesDropped": stats.BytesDropped,
		"truncated":    stats.Truncated,
		"fromSeq":      int64(stats.FromSeq),
		"nextSeq":      int64(stats.NextSeq),
	}, nil
}

// --- session.history.setPolicy ---
// Requires Plan Before Apply via dispatcher (high-risk).
// This handler is registered but dispatcher will intercept and require plan.

func historySetPolicy(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p struct {
		SessionID string              `json:"sessionId"`
		History   types.HistoryPolicy `json:"history"`
	}
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

	oldPolicy := sess.HistoryPolicy
	sess.HistoryPolicy = effectiveHistoryPolicy(&p.History)

	return map[string]interface{}{
		"sessionId": string(sess.ID),
		"oldPolicy": oldPolicy,
		"newPolicy": sess.HistoryPolicy,
	}, nil
}

// --- session.history.clear.plan ---
// Creates a plan for clearing history. Requires plan-before-apply.

func historyClearPlan(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p types.ClearHistoryRequest
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.SessionID == "" {
		return nil, fmt.Errorf("sessionId is required")
	}

	if deps.History == nil {
		return nil, fmt.Errorf("history store not available")
	}

	sess := deps.Sessions.Get(p.SessionID)
	if sess == nil {
		return nil, fmt.Errorf("session not found: %s", p.SessionID)
	}

	stats, err := deps.History.Stats(p.SessionID)
	if err != nil {
		stats = &types.HistoryStats{SessionID: p.SessionID}
	}

	return map[string]interface{}{
		"sessionId":           string(p.SessionID),
		"action":              "session.history.clear",
		"willDeleteStreams":   p.Streams,
		"estimatedBytesFreed": stats.BytesStored,
		"risk":                "medium",
	}, nil
}

// --- session.history.clear.execute ---
// Executes a previously planned and approved history clear.

func historyClearExecute(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p struct {
		PlanID    string   `json:"planId"`
		SessionID string   `json:"sessionId"`
		Streams   []string `json:"streams,omitempty"`
	}
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	// Must come through plan
	if p.PlanID == "" && req.PlanID == "" {
		return nil, fmt.Errorf("clear.history requires an approved plan — use clear.plan first")
	}

	if p.SessionID == "" {
		return nil, fmt.Errorf("sessionId is required")
	}

	if deps.History == nil {
		return nil, fmt.Errorf("history store not available")
	}

	bytesFreed, err := deps.History.Clear(types.SessionID(p.SessionID), p.Streams)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"sessionId":  p.SessionID,
		"bytesFreed": bytesFreed,
		"clearedAt":  time.Now().UnixMilli(),
	}, nil
}

// --- stream.replay ---

func streamReplay(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p types.ReplayRequest
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.SessionID == "" {
		return nil, fmt.Errorf("sessionId is required")
	}

	if deps.History == nil {
		return nil, fmt.Errorf("history store not available")
	}

	events, err := deps.History.Replay(p.SessionID, p.StreamType, p.FromSeq)
	if err != nil {
		if history.IsHistoryDisabled(err) {
			return map[string]interface{}{
				"sessionId": string(p.SessionID),
				"error":     err.Error(),
				"code":      "HISTORY_DISABLED",
			}, nil
		}
		if history.IsRangeTruncated(err) {
			// Return partial result with truncation warning
			out := make([]map[string]interface{}, len(events))
			for i, evt := range events {
				out[i] = eventToMap(evt)
			}
			return map[string]interface{}{
				"sessionId": string(p.SessionID),
				"events":    out,
				"truncated": true,
				"warning":   err.Error(),
			}, nil
		}
		return nil, err
	}

	out := make([]map[string]interface{}, len(events))
	for i, evt := range events {
		out[i] = eventToMap(evt)
	}
	return map[string]interface{}{
		"sessionId": string(p.SessionID),
		"events":    out,
		"count":     len(out),
	}, nil
}

// --- stream.tail ---

func streamTail(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p types.TailRequest
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.SessionID == "" {
		return nil, fmt.Errorf("sessionId is required")
	}
	if p.Lines <= 0 {
		p.Lines = 100
	}

	if deps.History == nil {
		return nil, fmt.Errorf("history store not available")
	}

	events, err := deps.History.Tail(p.SessionID, p.StreamType, p.Lines)
	if err != nil {
		if history.IsHistoryDisabled(err) {
			return map[string]interface{}{
				"sessionId": string(p.SessionID),
				"error":     err.Error(),
				"code":      "HISTORY_DISABLED",
			}, nil
		}
		return nil, err
	}

	out := make([]map[string]interface{}, len(events))
	for i, evt := range events {
		out[i] = eventToMap(evt)
	}
	return map[string]interface{}{
		"sessionId": string(p.SessionID),
		"events":    out,
		"count":     len(out),
	}, nil
}

// --- session.history.list (list sessions with history available) ---

func historyList(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	sessions := deps.Sessions.List()
	out := make([]map[string]interface{}, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, map[string]interface{}{
			"sessionId":    string(s.ID),
			"pluginId":     string(s.PluginID),
			"state":        s.State,
			"command":      s.Command,
			"historyMode":  s.HistoryPolicy.Mode,
			"historyBytes": s.HistoryPolicy.MaxBytes,
		})
	}
	return map[string]interface{}{"sessions": out}, nil
}

// --- helpers ---

func eventToMap(evt types.HistoryEvent) map[string]interface{} {
	m := map[string]interface{}{
		"eventSeq":  int64(evt.EventSeq),
		"type":      evt.Type,
		"timestamp": evt.Timestamp,
	}
	if evt.Stream != "" {
		m["stream"] = evt.Stream
	}
	if evt.Data != "" {
		m["data"] = evt.Data
	}
	if evt.Type == "exited" {
		m["exitCode"] = evt.ExitCode
	}
	return m
}
