package executor

import (
	"github.com/user/sessionnode/go-core/internal/logs"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// ─── logs.tail ───────────────────────────────────────────────────────────────

type logsTailPayload struct {
	Source string `json:"source"` // "core" | "plugin" | "system" | "session"
	Lines  int    `json:"lines"`
	Level  string `json:"level,omitempty"` // optional filter
}

type logLineEntry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Source    string `json:"source"`
	Message   string `json:"message"`
}

func logsTail(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p logsTailPayload
	_ = decodePayload(req.Payload, &p)

	limit := clampLimit(p.Lines, 100, 1000)

	lines := collectLogLines(deps, p.Source, p.Level, limit)
	entries := collectLogBufferEntries(deps, p.Source, p.Level, limit)
	return map[string]interface{}{"lines": lines, "entries": entries}, nil
}

// ─── logs.query ──────────────────────────────────────────────────────────────

type logsQueryPayload struct {
	Source   string `json:"source"`
	PluginID string `json:"pluginId,omitempty"`
	Level    string `json:"level,omitempty"`
	Limit    int    `json:"limit"`
}

type logEntry struct {
	Timestamp int64  `json:"timestamp"`
	Level     string `json:"level"`
	Source    string `json:"source"`
	PluginID  string `json:"pluginId,omitempty"`
	Message   string `json:"message"`
}

func logsQuery(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p logsQueryPayload
	_ = decodePayload(req.Payload, &p)

	limit := clampLimit(p.Limit, 100, 1000)

	entries := collectLogEntries(deps, p.Source, p.PluginID, p.Level, limit)
	return map[string]interface{}{"entries": entries}, nil
}

// ─── audit.list ──────────────────────────────────────────────────────────────

type auditListPayload struct {
	TimeRange string `json:"timeRange,omitempty"` // "24h", "7d", "30d"
	Type      string `json:"type,omitempty"`
	Actor     string `json:"actor,omitempty"`
	Target    string `json:"target,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type auditEntry struct {
	AuditID   string                 `json:"auditId"`
	Timestamp int64                  `json:"timestamp"`
	EventType string                 `json:"eventType"`
	Actor     string                 `json:"actor"`
	Target    string                 `json:"target"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

func auditList(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p auditListPayload
	_ = decodePayload(req.Payload, &p)

	limit := clampLimit(p.Limit, 100, 1000)

	entries := collectAuditEntries(deps, p.Type, p.Actor, p.Target, limit)
	return map[string]interface{}{"entries": entries}, nil
}

// ─── collectors ─────────────────────────────────────────────────────────────

func collectLogLines(deps *Deps, source string, level string, limit int) []logLineEntry {
	if deps == nil || deps.LogBuffer == nil {
		return []logLineEntry{}
	}
	entries := deps.LogBuffer.Tail(source, level, limit)
	out := make([]logLineEntry, len(entries))
	for i, e := range entries {
		out[i] = logLineEntry{
			Timestamp: formatTimestamp(e.Timestamp),
			Level:     e.Level,
			Source:    e.Source,
			Message:   e.Message,
		}
	}
	return out
}

func collectLogBufferEntries(deps *Deps, source string, level string, limit int) []logs.Entry {
	if deps == nil || deps.LogBuffer == nil {
		return []logs.Entry{}
	}
	return deps.LogBuffer.Tail(source, level, limit)
}

func collectLogEntries(deps *Deps, source string, pluginID string, level string, limit int) []logEntry {
	if deps == nil || deps.LogBuffer == nil {
		return []logEntry{}
	}
	entries := deps.LogBuffer.Query(source, pluginID, level, limit)
	out := make([]logEntry, len(entries))
	for i, e := range entries {
		out[i] = logEntry{
			Timestamp: e.Timestamp,
			Level:     e.Level,
			Source:    e.Source,
			PluginID:  e.PluginID,
			Message:   e.Message,
		}
	}
	return out
}

func collectAuditEntries(deps *Deps, eventType string, actor string, target string, limit int) []auditEntry {
	if deps == nil || deps.AuditStore == nil {
		return []auditEntry{}
	}
	records := deps.AuditStore.List(eventType, actor, target, limit)
	out := make([]auditEntry, len(records))
	for i, r := range records {
		out[i] = auditEntry{
			AuditID:   r.AuditID,
			Timestamp: r.Timestamp,
			EventType: r.EventType,
			Actor:     r.Actor,
			Target:    r.Target,
			Metadata:  r.Metadata,
		}
	}
	return out
}

// ─── helpers ────────────────────────────────────────────────────────────────

func clampLimit(n, defaultVal, maxVal int) int {
	if n <= 0 {
		return defaultVal
	}
	if n > maxVal {
		return maxVal
	}
	return n
}

func formatTimestamp(ts int64) string {
	return fmtTimestamp(ts)
}
