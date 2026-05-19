package wsconn

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/user/sessionnode/go-core/pkg/protocol"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// Registry tracks WebSocket write channels by session ID for server-to-client push.
// Each entry holds a channel that feeds into a single connection's write goroutine,
// so concurrent pushes are serialized per connection.
type Registry struct {
	mu    sync.RWMutex
	conns map[types.SessionID]chan<- []byte
}

// NewRegistry creates an empty connection registry.
func NewRegistry() *Registry {
	return &Registry{conns: make(map[types.SessionID]chan<- []byte)}
}

// Register binds a session ID to a write channel.
func (r *Registry) Register(sid types.SessionID, ch chan<- []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.conns[sid] = ch
}

// Unregister removes a session-to-channel mapping.
func (r *Registry) Unregister(sid types.SessionID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.conns, sid)
}

// Push sends a json-serialized Message to the channel associated with the session.
func (r *Registry) Push(sid types.SessionID, msg *protocol.Message) {
	r.mu.RLock()
	ch, ok := r.conns[sid]
	r.mu.RUnlock()
	if !ok {
		return
	}
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[wsconn] marshal error: %v", err)
		return
	}
	select {
	case ch <- data:
	default:
		log.Printf("[wsconn] write channel full for session %s, dropping message", sid)
	}
}

// PushChunk sends a stream.chunk message.
func (r *Registry) PushChunk(sid types.SessionID, streamType string, seq types.EventSeq, data string) {
	r.Push(sid, protocol.NewStreamChunk(sid, streamType, seq, data))
}

// PushSessionEvent sends a session.event message.
func (r *Registry) PushSessionEvent(sid types.SessionID, seq types.EventSeq, eventType string, payload interface{}) {
	raw, _ := json.Marshal(payload)
	r.Push(sid, protocol.NewSessionEvent(sid, seq, eventType, raw))
}

// Broadcast sends a message to all registered sessions.
func (r *Registry) Broadcast(msg *protocol.Message) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.conns) == 0 {
		return
	}
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[wsconn] broadcast marshal error: %v", err)
		return
	}
	for _, ch := range r.conns {
		select {
		case ch <- data:
		default:
		}
	}
}

// RemoveAllForCh removes all session mappings pointing to the given channel.
func (r *Registry) RemoveAllForCh(ch chan<- []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for sid, c := range r.conns {
		if c == ch {
			delete(r.conns, sid)
		}
	}
}
