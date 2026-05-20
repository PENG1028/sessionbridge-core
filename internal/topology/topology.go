// Package topology manages peer-to-peer connections between Go Core instances.
//
// Each Go Core instance is an autonomous process with its own sessions, processes,
// and state. The topology layer provides unified WebSocket-based forwarding so that
// any node can dispatch capability requests to any other node — local or remote.
//
// "local" tag: purely a display-grouping hint for nodes on the same machine.
// Forwarding logic for local and remote peers is identical.
package topology

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/user/sessionnode/go-core/internal/dispatcher"
	"github.com/user/sessionnode/go-core/internal/executor"
	"github.com/user/sessionnode/go-core/pkg/protocol"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// --- Configuration ---

// Config defines the local identity and list of peers to connect to.
type Config struct {
	LocalID   types.NodeID
	LocalName string         // from node.name, used as base for display naming
	Peers     []PeerConfig
}

// PeerConfig describes a remote peer to connect to.
type PeerConfig struct {
	ID      types.NodeID `json:"id"`
	Address string       `json:"address"`          // "host:port"
	Tags    []string     `json:"tags,omitempty"`   // e.g. ["local"]
}

// --- Status constants ---

const (
	StatusLocal        = "local"
	StatusConnected    = "connected"
	StatusDisconnected = "disconnected"
	StatusConnecting   = "connecting"
)

// --- Peer (runtime state) ---

// Peer holds the runtime state for a single known node.
type Peer struct {
	ID      types.NodeID
	Address string
	Tags    []string

	status  string
	conn    *websocket.Conn
	writeCh chan []byte
	mu      sync.RWMutex
}

func newPeer(id types.NodeID, address string, tags []string, status string) *Peer {
	return &Peer{
		ID:      id,
		Address: address,
		Tags:    tags,
		status:  status,
	}
}

func (p *Peer) getStatus() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.status
}

// --- PeerTopology ---

// StreamChunkHandler is called when a stream.chunk or session.event message
// arrives from a peer. The handler must route the chunk to local subscribers
// (typically via wsconn.Registry.PushChunk / PushSessionEvent).
type StreamChunkHandler func(msg *protocol.Message)

// PeerTopology implements dispatcher.Topology and executor.NodeLister.
//
// It maintains WebSocket connections to all configured peers and provides
// a Forward function that serialises capability requests over the wire.
type PeerTopology struct {
	localID   types.NodeID
	localName string
	peers     map[types.NodeID]*Peer

	pending   map[types.RequestID]chan *types.CapabilityResponse
	pendingMu sync.Mutex

	streamChunkHandler StreamChunkHandler

	mu  sync.RWMutex
	log *log.Logger
}

// New creates a PeerTopology. The local node is registered automatically
// with the "local" tag. Configured peers are registered in disconnected state.
func New(cfg Config) *PeerTopology {
	pt := &PeerTopology{
		localID:   cfg.LocalID,
		localName: cfg.LocalName,
		peers:     make(map[types.NodeID]*Peer),
		pending:   make(map[types.RequestID]chan *types.CapabilityResponse),
		log:       log.New(log.Writer(), "[topology] ", log.LstdFlags),
	}

	// Register local node
	pt.peers[cfg.LocalID] = newPeer(cfg.LocalID, "", []string{"local"}, StatusLocal)

	// Register configured peers
	for _, p := range cfg.Peers {
		if p.ID == cfg.LocalID {
			continue // skip self
		}
		pt.peers[p.ID] = newPeer(p.ID, p.Address, p.Tags, StatusDisconnected)
	}

	return pt
}

// SetStreamChunkHandler registers the handler for incoming stream.chunk and
// session.event messages from peers. When nil (default), the messages are
// logged and dropped gracefully.
func (pt *PeerTopology) SetStreamChunkHandler(h StreamChunkHandler) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.streamChunkHandler = h
}

// Start connects to all non-local peers in the background.
// Blocks until ctx is cancelled, then shuts down all connections.
func (pt *PeerTopology) Start(ctx context.Context) {
	for id, peer := range pt.peers {
		if id == pt.localID {
			continue
		}
		go pt.connectLoop(ctx, peer)
	}
	<-ctx.Done()
	pt.Shutdown()
}

// Shutdown closes all peer connections immediately.
func (pt *PeerTopology) Shutdown() {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	for _, peer := range pt.peers {
		peer.mu.Lock()
		if peer.conn != nil {
			peer.conn.Close()
		}
		peer.mu.Unlock()
	}
}

// ---------------------------------------------------------------------------
// dispatcher.Topology implementation
// ---------------------------------------------------------------------------

// Get returns a NodeTarget that can forward requests to the given node.
// Returns an error if the node is unknown or is the local node.
func (pt *PeerTopology) Get(nodeID types.NodeID) (*dispatcher.NodeTarget, error) {
	pt.mu.RLock()
	peer, ok := pt.peers[nodeID]
	pt.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("node %s not found in topology", nodeID)
	}
	if nodeID == pt.localID {
		return nil, fmt.Errorf("node %s is the local node, no forwarding needed", nodeID)
	}

	return &dispatcher.NodeTarget{
		ID: nodeID,
		Forward: func(req *types.CapabilityRequest) (*types.CapabilityResponse, error) {
			return pt.forward(peer, req)
		},
	}, nil
}

// ---------------------------------------------------------------------------
// executor.NodeLister implementation
// ---------------------------------------------------------------------------

// ListNodes returns all known nodes with their current status and display name.
func (pt *PeerTopology) ListNodes() []executor.NodeInfo {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	// Collect local-tagged peers for display numbering
	var localPeers []*Peer
	for _, p := range pt.peers {
		if hasTag(p.Tags, "local") {
			localPeers = append(localPeers, p)
		}
	}
	sort.Slice(localPeers, func(i, j int) bool {
		return localPeers[i].ID < localPeers[j].ID
	})

	displayNames := computeLocalDisplayNames(localPeers, pt.localName)

	out := make([]executor.NodeInfo, 0, len(pt.peers))
	for _, p := range pt.peers {
		info := executor.NodeInfo{
			ID:      p.ID,
			Name:    peerName(p, pt.localID, pt.localName),
			Address: p.Address,
			Tags:    p.Tags,
			Status:  p.getStatus(),
		}

		if dn, ok := displayNames[p.ID]; ok {
			info.DisplayName = dn
		} else {
			info.DisplayName = string(p.ID)
		}

		out = append(out, info)
	}

	return out
}

// ---------------------------------------------------------------------------
// Forwarding
// ---------------------------------------------------------------------------

// forward sends a capability request to a peer via WebSocket and waits for
// the response. The request is correlated by RequestID.
func (pt *PeerTopology) forward(peer *Peer, req *types.CapabilityRequest) (*types.CapabilityResponse, error) {
	peer.mu.RLock()
	conn := peer.conn
	writeCh := peer.writeCh
	peer.mu.RUnlock()

	if conn == nil {
		return nil, fmt.Errorf("node %s is not connected", peer.ID)
	}

	// Build forwarded message preserving original actor info
	var payload json.RawMessage
	if req.Payload != nil {
		payload = req.Payload
	}
	msg := &protocol.Message{
		Type:         protocol.MsgTypeActionRequest,
		RequestID:    req.RequestID,
		PluginID:     req.PluginID,
		NodeID:       pt.localID,
		Capability:   req.Capability,
		TargetNodeID: req.TargetNodeID,
		Payload:      payload,
		Timestamp:    time.Now().UnixMilli(),
		ActorType:    "node", // marks as node-to-node, skips token auth
		ActorID:      string(pt.localID),
	}

	data, err := msg.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("marshal forward message: %w", err)
	}

	// Register pending response channel
	ch := make(chan *types.CapabilityResponse, 1)
	pt.pendingMu.Lock()
	pt.pending[req.RequestID] = ch
	pt.pendingMu.Unlock()

	defer func() {
		pt.pendingMu.Lock()
		delete(pt.pending, req.RequestID)
		pt.pendingMu.Unlock()
	}()

	// Send via peer's write channel
	select {
	case writeCh <- data:
	default:
		return nil, fmt.Errorf("write channel full for node %s", peer.ID)
	}

	// Wait for response with timeout
	select {
	case resp := <-ch:
		return resp, nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("timeout waiting for response from node %s", peer.ID)
	}
}

// HandleMessage processes an incoming message from a peer's WebSocket read loop.
func (pt *PeerTopology) HandleMessage(senderID types.NodeID, data []byte) {
	msg, err := protocol.UnmarshalMessage(data)
	if err != nil {
		return
	}

	switch msg.Type {
	case protocol.MsgTypeActionResponse:
		pt.pendingMu.Lock()
		ch, ok := pt.pending[msg.RequestID]
		pt.pendingMu.Unlock()
		if !ok {
			return // nobody waiting (timeout already fired or unsolicited)
		}

		resp := &types.CapabilityResponse{
			RequestID: msg.RequestID,
			OK:        msg.OK,
		}
		if msg.Payload != nil {
			resp.Payload = msg.Payload
		}
		if msg.Error != nil {
			resp.Error = &types.CoreError{Code: msg.Error.Code, Message: msg.Error.Message}
		}
		ch <- resp

	case protocol.MsgTypeStreamChunk, protocol.MsgTypeSessionEvent:
		pt.mu.RLock()
		h := pt.streamChunkHandler
		pt.mu.RUnlock()
		if h != nil {
			h(msg)
		} else {
			pt.log.Printf("stream chunk/event from %s dropped: no handler registered (type=%q session=%s)", senderID, msg.Type, msg.SessionID)
		}

	default:
		pt.log.Printf("unhandled message type %q from %s", msg.Type, senderID)
	}
}

// ---------------------------------------------------------------------------
// Connection management
// ---------------------------------------------------------------------------

// connectLoop attempts to maintain a persistent WebSocket connection to peer.
// It retries with exponential backoff on failure.
func (pt *PeerTopology) connectLoop(ctx context.Context, peer *Peer) {
	backoff := 1 * time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		peer.mu.Lock()
		peer.status = StatusConnecting
		peer.mu.Unlock()

		conn, _, err := websocket.DefaultDialer.Dial(
			fmt.Sprintf("ws://%s/ws", peer.Address), nil,
		)
		if err != nil {
			peer.mu.Lock()
			peer.status = StatusDisconnected
			peer.mu.Unlock()

			pt.log.Printf("connect to %s (%s) failed: %v, retry in %v", peer.ID, peer.Address, err, backoff)

			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}

			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		backoff = 1 * time.Second // reset on success

		writeCh := make(chan []byte, 64)
		stopCh := make(chan struct{})

		peer.mu.Lock()
		peer.conn = conn
		peer.writeCh = writeCh
		peer.status = StatusConnected
		peer.mu.Unlock()

		pt.log.Printf("connected to peer %s (%s)", peer.ID, peer.Address)

		// Write goroutine — reads from writeCh and sends over WebSocket
		go func() {
			for {
				select {
				case data, ok := <-writeCh:
					if !ok {
						return
					}
					if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
						return
					}
					if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
						return
					}
				case <-stopCh:
					return
				}
			}
		}()

		// Read — blocks in this goroutine until connection dies
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				break
			}
			pt.HandleMessage(peer.ID, data)
		}

		// Connection lost — tear down
		conn.Close()
		close(stopCh)

		peer.mu.Lock()
		peer.conn = nil
		peer.status = StatusDisconnected
		peer.mu.Unlock()
		close(writeCh)

		pt.log.Printf("disconnected from peer %s, reconnecting...", peer.ID)
	}
}

// ---------------------------------------------------------------------------
// Display naming
// ---------------------------------------------------------------------------

// computeLocalDisplayNames generates display names for local-tagged peers.
//
// Rules:
//   - Single local peer → displayName = localName
//   - Multiple local peers → displayName = localName + "-(N)"
//     sorted by node ID, starting from 1
func computeLocalDisplayNames(localPeers []*Peer, localName string) map[types.NodeID]string {
	out := make(map[types.NodeID]string, len(localPeers))
	if len(localPeers) == 0 {
		return out
	}

	if len(localPeers) == 1 {
		out[localPeers[0].ID] = localName
		return out
	}

	for i, p := range localPeers {
		out[p.ID] = fmt.Sprintf("%s-(%d)", localName, i+1)
	}
	return out
}

// peerName returns the human-readable name for a peer.
func peerName(p *Peer, localID types.NodeID, localName string) string {
	if p.ID == localID {
		return localName
	}
	return string(p.ID)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func hasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}
