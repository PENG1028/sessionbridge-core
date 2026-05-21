package executor

import (
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
	return map[string]interface{}{"lines": lines}, nil
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

// collectLogLines gathers log lines from the in-memory buffer.
// Phase 1: returns empty — no persistent log store yet.
func collectLogLines(deps *Deps, source string, level string, limit int) []logLineEntry {
	if deps == nil || deps.History == nil {
		return []logLineEntry{}
	}
	raw := deps.History.RecentLogLines(source, level, limit)
	if raw == nil {
		return []logLineEntry{}
	}
	if lines, ok := raw.([]logLineEntry); ok {
		return lines
	}
	return []logLineEntry{}
}

// collectLogEntries gathers structured log entries for plugin detail views.
// Phase 1: returns empty — no persistent log store yet.
func collectLogEntries(deps *Deps, source string, pluginID string, level string, limit int) []logEntry {
	if deps == nil || deps.History == nil {
		return []logEntry{}
	}
	raw := deps.History.RecentLogEntries(source, pluginID, level, limit)
	if raw == nil {
		return []logEntry{}
	}
	if entries, ok := raw.([]logEntry); ok {
		return entries
	}
	return []logEntry{}
}

// collectAuditEntries gathers audit trail entries from history or returns empty.
// Phase 1: minimal — no dedicated audit store; returns empty.
func collectAuditEntries(deps *Deps, eventType string, actor string, target string, limit int) []auditEntry {
	if deps == nil || deps.History == nil {
		return []auditEntry{}
	}
	raw := deps.History.RecentAuditEntries(eventType, actor, target, limit)
	if raw == nil {
		return []auditEntry{}
	}
	if entries, ok := raw.([]auditEntry); ok {
		return entries
	}
	return []auditEntry{}
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

