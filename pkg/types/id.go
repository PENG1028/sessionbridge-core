package types

// Typed IDs for the SessionNode identity system.
// Each concept gets its own type to prevent cross-ID confusion at compile time.

type NodeID string

func (id NodeID) Valid() bool     { return id != "" }
func (id NodeID) String() string  { return string(id) }

type SessionID string

func (id SessionID) Valid() bool    { return id != "" }
func (id SessionID) String() string { return string(id) }

type StreamID string

func (id StreamID) Valid() bool    { return id != "" }
func (id StreamID) String() string { return string(id) }

type RequestID string

func (id RequestID) Valid() bool    { return id != "" }
func (id RequestID) String() string { return string(id) }

type PluginID string

func (id PluginID) Valid() bool    { return id != "" }
func (id PluginID) String() string { return string(id) }

// EventSeq is a monotonically increasing sequence number for session events.
type EventSeq int64

func (s EventSeq) Valid() bool    { return s >= 0 }
func (s EventSeq) String() string { return int64Str(int64(s)) }

func int64Str(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	buf := make([]byte, 0, 20)
	for n > 0 {
		buf = append(buf, byte('0'+n%10))
		n /= 10
	}
	if neg {
		buf = append(buf, '-')
	}
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}

// ValidateNodeID returns true if the node ID is non-empty.
func ValidateNodeID(id NodeID) error {
	if !id.Valid() {
		return &CoreError{Code: "INVALID_NODE_ID", Message: "node ID must not be empty"}
	}
	return nil
}

// ValidateSessionID returns true if the session ID is non-empty.
func ValidateSessionID(id SessionID) error {
	if !id.Valid() {
		return &CoreError{Code: "INVALID_SESSION_ID", Message: "session ID must not be empty"}
	}
	return nil
}
