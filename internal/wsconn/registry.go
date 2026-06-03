package wsconn

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/user/sessionnode/go-core/pkg/protocol"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// Connection represents a single WebSocket client with its actor identity.
type Connection struct {
	ID          string
	WriteCh     chan<- []byte
	Actor       types.Actor
	ConnectedAt time.Time
	closed      atomic.Bool
}

// Subscription links a connection to a session's streams.
type Subscription struct {
	ID          string
	ConnID      string
	SessionID   types.SessionID
	StreamTypes []string // ["stdout"], ["stderr"], or ["stdout", "stderr"]
	PluginID    types.PluginID
	Actor       types.Actor
	FromSeq     types.EventSeq
	CreatedAt   time.Time
}

// Registry manages WebSocket connections and their stream subscriptions.
// Supports multi-subscriber sessions: output from a single session can fan
// out to multiple connections (same or different devices).
//
// All concurrent access is safe — the Registry uses a single RWMutex.
// Channel sends to individual connections are non-blocking:
// a slow or disconnected subscriber drops messages instead of blocking the
// process output loop.
type Registry struct {
	mu     sync.RWMutex
	conns  map[string]*Connection
	subs   map[string]*Subscription                     // subId → sub
	bySess map[types.SessionID]map[string]*Subscription // sessionId → subId → sub
	byConn map[string]map[string]*Subscription          // connId → subId → sub
	connID atomic.Int64
	subID  atomic.Int64
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		conns:  make(map[string]*Connection),
		subs:   make(map[string]*Subscription),
		bySess: make(map[types.SessionID]map[string]*Subscription),
		byConn: make(map[string]map[string]*Subscription),
	}
}

// RegisterConn creates a connection entry and returns it.
func (r *Registry) RegisterConn(writeCh chan<- []byte, actor types.Actor) *Connection {
	id := fmt.Sprintf("conn_%d", r.connID.Add(1))
	c := &Connection{
		ID:          id,
		WriteCh:     writeCh,
		Actor:       actor,
		ConnectedAt: time.Now(),
	}
	r.mu.Lock()
	r.conns[id] = c
	r.mu.Unlock()
	return c
}

// UnregisterConn removes a connection and all its subscriptions.
// This is safe to call even if the connection was already subscribed and
// then a new connection re-subscribed for the same session — it only
// removes subscriptions owned by this specific connection ID.
func (r *Registry) UnregisterConn(connID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if c, ok := r.conns[connID]; ok {
		c.closed.Store(true)
	}
	delete(r.conns, connID)

	for subID, sub := range r.byConn[connID] {
		delete(r.subs, subID)
		if sessSubs, ok := r.bySess[sub.SessionID]; ok {
			delete(sessSubs, subID)
			if len(sessSubs) == 0 {
				delete(r.bySess, sub.SessionID)
			}
		}
	}
	delete(r.byConn, connID)
}

// Subscribe registers a subscription from a connection to a session's streams.
// Returns the created Subscription. Does NOT deduplicate — if the same
// connection subscribes twice, two separate subscriptions are created
// (the client should track and unsubscribe as needed).
func (r *Registry) Subscribe(connID string, sid types.SessionID, streamTypes []string, pluginID types.PluginID, actor types.Actor, fromSeq types.EventSeq) *Subscription {
	typesCopy := make([]string, len(streamTypes))
	copy(typesCopy, streamTypes)

	sub := &Subscription{
		ID:          fmt.Sprintf("sub_%d", r.subID.Add(1)),
		ConnID:      connID,
		SessionID:   sid,
		StreamTypes: typesCopy,
		PluginID:    pluginID,
		Actor:       actor,
		FromSeq:     fromSeq,
		CreatedAt:   time.Now(),
	}

	r.mu.Lock()
	r.subs[sub.ID] = sub
	if r.bySess[sid] == nil {
		r.bySess[sid] = make(map[string]*Subscription)
	}
	r.bySess[sid][sub.ID] = sub
	if r.byConn[connID] == nil {
		r.byConn[connID] = make(map[string]*Subscription)
	}
	r.byConn[connID][sub.ID] = sub
	r.mu.Unlock()

	return sub
}

// Unsubscribe removes a single subscription by ID.
func (r *Registry) Unsubscribe(subID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	sub, ok := r.subs[subID]
	if !ok {
		return
	}
	delete(r.subs, subID)
	if sessSubs, ok := r.bySess[sub.SessionID]; ok {
		delete(sessSubs, subID)
		if len(sessSubs) == 0 {
			delete(r.bySess, sub.SessionID)
		}
	}
	if connSubs, ok := r.byConn[sub.ConnID]; ok {
		delete(connSubs, subID)
	}
}

// SubscriptionsBySession returns all subscriptions for a session (copy).
func (r *Registry) SubscriptionsBySession(sid types.SessionID) []*Subscription {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sessSubs := r.bySess[sid]
	if len(sessSubs) == 0 {
		return nil
	}
	out := make([]*Subscription, 0, len(sessSubs))
	for _, sub := range sessSubs {
		out = append(out, sub)
	}
	return out
}

// SubscriptionsByConn returns all subscriptions for a connection (copy).
func (r *Registry) SubscriptionsByConn(connID string) []*Subscription {
	r.mu.RLock()
	defer r.mu.RUnlock()
	connSubs := r.byConn[connID]
	if len(connSubs) == 0 {
		return nil
	}
	out := make([]*Subscription, 0, len(connSubs))
	for _, sub := range connSubs {
		out = append(out, sub)
	}
	return out
}

// PushChunk broadcasts a stream.chunk message to every connected client.
//
// In the current single-tenant architecture all clients receive all push
// messages. This ensures the App UI SSE endpoint (which holds a long-lived
// WS connection that never explicitly subscribes) receives real-time process
// output without requiring a subscription round-trip.
//
// Non-blocking per connection: if a connection's write channel is full the
// message is dropped and a diagnostic log is emitted. A blocked connection
// cannot delay other connections or the process output loop.
func (r *Registry) PushChunk(sid types.SessionID, streamType string, seq types.EventSeq, data string) {
	msg := protocol.NewStreamChunk(sid, streamType, seq, data)
	r.Broadcast(msg)
}

// PushSessionEvent broadcasts a session.event message to every connected
// client. See PushChunk for the rationale on broadcast-vs-subscribers.
func (r *Registry) PushSessionEvent(sid types.SessionID, seq types.EventSeq, eventType string, payload interface{}) {
	raw, _ := json.Marshal(payload)
	msg := protocol.NewSessionEvent(sid, seq, eventType, raw)
	r.Broadcast(msg)
}

func (r *Registry) pushToSubscribers(sid types.SessionID, streamType string, msg *protocol.Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[wsconn] marshal error: %v", err)
		return
	}

	r.mu.RLock()
	subs := r.bySess[sid]
	if len(subs) == 0 {
		r.mu.RUnlock()
		return
	}
	// Snapshot subscriber list under lock, then send without lock
	list := make([]*Subscription, 0, len(subs))
	for _, sub := range subs {
		if streamType == "" {
			list = append(list, sub)
			continue
		}
		for _, st := range sub.StreamTypes {
			if st == streamType {
				list = append(list, sub)
				break
			}
		}
	}
	r.mu.RUnlock()

	for _, sub := range list {
		r.mu.RLock()
		conn := r.conns[sub.ConnID]
		r.mu.RUnlock()
		if conn == nil || conn.closed.Load() {
			continue
		}
		select {
		case conn.WriteCh <- data:
		default:
			log.Printf("[wsconn] drop msg for subscriber %s (session %s, stream %s): channel full", sub.ID, sid, streamType)
		}
	}
}

// Broadcast sends a message to every connected connection.
func (r *Registry) Broadcast(msg *protocol.Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[wsconn] broadcast marshal error: %v", err)
		return
	}

	r.mu.RLock()
	list := make([]*Connection, 0, len(r.conns))
	for _, c := range r.conns {
		list = append(list, c)
	}
	r.mu.RUnlock()

	for _, c := range list {
		if c.closed.Load() {
			continue
		}
		select {
		case c.WriteCh <- data:
		default:
		}
	}
}

// ConnectionCount returns the number of active connections.
func (r *Registry) ConnectionCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.conns)
}

// SubscriberCount returns the number of subscriptions for a session.
func (r *Registry) SubscriberCount(sid types.SessionID) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.bySess[sid])
}

// GetConn returns a connection by ID, or nil.
func (r *Registry) GetConn(id string) *Connection {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.conns[id]
}
