package session

import (
	"github.com/user/sessionnode/go-core/pkg/types"
)

// State constants for Session.
const (
	StateCreated = "created"
	StateRunning = "running"
	StateExited  = "exited"
	StateError   = "error"
)

// Stream holds a rolling buffer of output for a single stream type (stdout/stderr/stdin).
type Stream struct {
	Type    string `json:"type"`
	Buffer  []byte `json:"buffer"`
	MaxSize int    `json:"maxSize"`
}

// NewStream creates a Stream with the given type and max buffer size (default 64KB).
func NewStream(streamType string, maxSize int) *Stream {
	if maxSize <= 0 {
		maxSize = 64 * 1024
	}
	return &Stream{Type: streamType, Buffer: make([]byte, 0, maxSize), MaxSize: maxSize}
}

// Write appends data to the stream buffer, rolling off old data if it exceeds MaxSize.
func (s *Stream) Write(data []byte) {
	if len(data) == 0 {
		return
	}
	// If the data itself exceeds MaxSize, keep only the last MaxSize bytes.
	if len(data) > s.MaxSize {
		data = data[len(data)-s.MaxSize:]
	}
	total := len(s.Buffer) + len(data)
	if total > s.MaxSize {
		excess := total - s.MaxSize
		if excess >= len(s.Buffer) {
			s.Buffer = s.Buffer[:0]
		} else {
			s.Buffer = s.Buffer[excess:]
		}
	}
	s.Buffer = append(s.Buffer, data...)
}

// Read returns a copy of the current buffer.
func (s *Stream) Read() []byte {
	out := make([]byte, len(s.Buffer))
	copy(out, s.Buffer)
	return out
}

// Session represents a running or completed shell/process session.
type Session struct {
	ID        types.SessionID  `json:"id"`
	PluginID  types.PluginID   `json:"pluginId"`
	State     string           `json:"state"`
	Command   string           `json:"command,omitempty"`
	Cwd       string           `json:"cwd,omitempty"`
	CreatedAt int64            `json:"createdAt"`
	Streams   map[string]*Stream `json:"streams,omitempty"`
}

// NewSession creates a new Session with the given parameters and default streams.
func NewSession(id types.SessionID, pluginID types.PluginID, command, cwd string, createdAt int64) *Session {
	return &Session{
		ID:        id,
		PluginID:  pluginID,
		State:     StateCreated,
		Command:   command,
		Cwd:       cwd,
		CreatedAt: createdAt,
		Streams: map[string]*Stream{
			"stdout": NewStream("stdout", 64*1024),
			"stderr": NewStream("stderr", 64*1024),
			"stdin":  NewStream("stdin", 64*1024),
		},
	}
}
