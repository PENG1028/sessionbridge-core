package session

import (
	"fmt"

	"github.com/user/sessionnode/go-core/pkg/types"
)

// DefaultHistoryPolicy is the safe default applied when no policy is provided.
var DefaultHistoryPolicy = types.DefaultHistoryPolicy

// State constants for Session lifecycle.
const (
	StateCreated     = "created"
	StateRunning     = "running"
	StateInterrupted = "interrupted"
	StateResumable   = "resumable"
	StateExited      = "exited"
	StateError       = "error"
	StateClosed      = "closed"
)

// validTransitions maps each state to the set of states it may transition to.
var validTransitions = map[string]map[string]bool{
	StateCreated:     {StateRunning: true, StateError: true, StateClosed: true},
	StateRunning:     {StateExited: true, StateError: true, StateInterrupted: true, StateClosed: true},
	StateInterrupted: {StateResumable: true, StateClosed: true, StateRunning: true},
	StateResumable:   {StateRunning: true, StateClosed: true, StateExited: true},
	StateExited:      {StateClosed: true},
	StateError:       {StateClosed: true},
	StateClosed:      {},
}

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

// Session represents a running or completed shell/process session
// with a full lifecycle state machine.
type Session struct {
	ID            types.SessionID     `json:"id"`
	PluginID      types.PluginID      `json:"pluginId"`
	State         string              `json:"state"`
	Command       string              `json:"command,omitempty"`
	Cwd           string              `json:"cwd,omitempty"`
	CreatedAt     int64               `json:"createdAt"`
	UpdatedAt     int64               `json:"updatedAt,omitempty"`
	Streams       map[string]*Stream  `json:"streams,omitempty"`
	HistoryPolicy types.HistoryPolicy `json:"historyPolicy,omitempty"`
}

// NewSession creates a new Session with the given parameters and default streams.
func NewSession(id types.SessionID, pluginID types.PluginID, command, cwd string, createdAt int64) *Session {
	return NewSessionWithPolicy(id, pluginID, command, cwd, createdAt, DefaultHistoryPolicy())
}

// NewSessionWithPolicy creates a new Session with the given history policy.
func NewSessionWithPolicy(id types.SessionID, pluginID types.PluginID, command, cwd string, createdAt int64, hp types.HistoryPolicy) *Session {
	return &Session{
		ID:            id,
		PluginID:      pluginID,
		State:         StateCreated,
		Command:       command,
		Cwd:           cwd,
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
		HistoryPolicy: hp,
		Streams: map[string]*Stream{
			"stdout": NewStream("stdout", 64*1024),
			"stderr": NewStream("stderr", 64*1024),
			"stdin":  NewStream("stdin", 64*1024),
		},
	}
}

// TransitionState validates and applies a state transition.
// Returns an error if the transition is not allowed by the state machine.
func (s *Session) TransitionState(newState string, updatedAt int64) error {
	allowed, ok := validTransitions[s.State]
	if !ok {
		return fmt.Errorf("unknown current state: %s", s.State)
	}
	if !allowed[newState] {
		return fmt.Errorf("invalid state transition: %s → %s", s.State, newState)
	}
	s.State = newState
	s.UpdatedAt = updatedAt
	return nil
}

// IsTerminal returns true if the session has reached a terminal state.
func (s *Session) IsTerminal() bool {
	return s.State == StateExited || s.State == StateError || s.State == StateClosed
}

// IsResumable returns true if the session can be resumed.
// Only sessions in resumable or interrupted state can be resumed.
func (s *Session) IsResumable() bool {
	return s.State == StateResumable || s.State == StateInterrupted
}

// CanStream returns true if the session can produce/receive stream data.
func (s *Session) CanStream() bool {
	return s.State == StateRunning || s.State == StateCreated
}

// Interrupt marks a running session as interrupted (e.g. network disconnect).
// Data is preserved so the session can later enter resumable state.
func (s *Session) Interrupt(updatedAt int64) error {
	return s.TransitionState(StateInterrupted, updatedAt)
}

// MakeResumable transitions from interrupted to resumable state.
// This means the session data is available for replay but the session
// is not actively running.
func (s *Session) MakeResumable(updatedAt int64) error {
	return s.TransitionState(StateResumable, updatedAt)
}

// Close marks the session as closed regardless of current state.
// This is a cleanup operation, not a failure.
func (s *Session) Close(updatedAt int64) error {
	return s.TransitionState(StateClosed, updatedAt)
}
