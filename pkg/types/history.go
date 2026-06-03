package types

// HistoryMode constants.
const (
	HistoryModeDisabled  = "disabled"
	HistoryModeMemory    = "memory"
	HistoryModeDisk      = "disk"
	HistoryModeEncrypted = "encrypted-disk"
)

// HistoryRedaction constants.
const (
	HistoryRedactionNone   = "none"
	HistoryRedactionBasic  = "basic"
	HistoryRedactionPlugin = "plugin"
)

// HistoryVisibility constants.
const (
	HistoryVisSameActor  = "same-actor"
	HistoryVisSamePlugin = "same-plugin"
	HistoryVisAuthorized = "authorized"
)

// DefaultHistoryPolicy returns a safe default policy.
// - Memory ring buffer enabled
// - stdout + stderr recorded
// - stdin NOT recorded
// - No disk persistence
// - 100MB max per session
// - Max age 24h
// - No redaction
// - Authorized visibility
func DefaultHistoryPolicy() HistoryPolicy {
	return HistoryPolicy{
		Enabled:     true,
		Mode:        HistoryModeMemory,
		Streams:     []string{"stdout", "stderr"},
		MaxBytes:    100 * 1024 * 1024, // 100 MB
		MaxAge:      "24h",
		Redaction:   HistoryRedactionNone,
		Visibility:  HistoryVisAuthorized,
		ClearOnStop: false,
	}
}

// HistoryPolicy controls session history retention and replay behavior.
// It is set at session creation and cannot be changed mid-session
// (use session.history.setPolicy for changes).
type HistoryPolicy struct {
	Enabled     bool     `json:"enabled"`
	Mode        string   `json:"mode"`
	Streams     []string `json:"streams"`
	MaxBytes    int64    `json:"maxBytes"`
	MaxAge      string   `json:"maxAge"`
	Redaction   string   `json:"redaction"`
	Visibility  string   `json:"visibility"`
	ClearOnStop bool     `json:"clearOnStop"`
}

// HistoryEvent is a single recorded event in session history.
type HistoryEvent struct {
	EventSeq  EventSeq `json:"eventSeq"`
	Type      string   `json:"type"` // "session.created", "stream.stdout", "stream.stderr", "session.stopped", etc.
	Stream    string   `json:"stream,omitempty"`
	Data      string   `json:"data,omitempty"`
	Timestamp int64    `json:"timestamp"`
	ExitCode  int      `json:"exitCode,omitempty"`
}

// HistoryStats provides summary information about session history.
type HistoryStats struct {
	SessionID    SessionID `json:"sessionId"`
	Mode         string    `json:"mode"`
	EventCount   int64     `json:"eventCount"`
	BytesStored  int64     `json:"bytesStored"`
	BytesDropped int64     `json:"bytesDropped"`
	Truncated    bool      `json:"truncated"`
	FromSeq      EventSeq  `json:"fromSeq"`
	NextSeq      EventSeq  `json:"nextSeq"`
}

// ReplayRequest is the payload for stream.replay.
type ReplayRequest struct {
	SessionID  SessionID `json:"sessionId"`
	StreamType string    `json:"streamType"` // "stdout", "stderr", "stdin", "", or "event"
	FromSeq    EventSeq  `json:"fromSeq"`
	MaxBytes   int64     `json:"maxBytes,omitempty"` // limit response size
}

// TailRequest is the payload for stream.tail.
type TailRequest struct {
	SessionID  SessionID `json:"sessionId"`
	StreamType string    `json:"streamType"`
	Lines      int       `json:"lines"`
}

// ClearHistoryRequest is the payload for session.history.clear.plan.
type ClearHistoryRequest struct {
	SessionID SessionID `json:"sessionId"`
	Streams   []string  `json:"streams,omitempty"` // empty = all streams
}
