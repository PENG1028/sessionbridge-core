package topology

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/PENG1028/sessionbridge-core/pkg/types"
)

// ─── mDNS-style LAN Discovery (zero external dependencies) ──────────────
//
// Uses UDP multicast on 224.0.0.251:5353 (standard mDNS port) to:
//   - Broadcast our presence as a JSON packet
//   - Listen for other SessionBridge nodes
//   - Extract nodeId, fingerprint, address
//
// No external DNS library needed — we use a simple JSON protocol on the
// standard mDNS port. Compatible with any platform that supports UDP
// multicast (all modern OSes, including Tailscale networks).

const (
	mdnsAddr = "224.0.0.251:5353"
)

// announcePayload is the JSON message broadcast by each node.
type announcePayload struct {
	Service     string `json:"_svc"`     // "sessionbridge"
	Version     int    `json:"_ver"`     // 1
	NodeID      string `json:"nodeId"`
	Fingerprint string `json:"fp"`
	Port        int    `json:"port"`
	Hostname    string `json:"host,omitempty"`
}

// DiscoveredPeer represents a node found via LAN discovery.
type DiscoveredPeer struct {
	NodeID      types.NodeID
	Address     string // "ip:port"
	Fingerprint string
	Tags        []string
}

// DiscoveryCallback is called when a new peer is discovered.
type DiscoveryCallback func(peer DiscoveredPeer)

// Discoverer manages LAN discovery via UDP multicast.
type Discoverer struct {
	port        int
	nodeID      types.NodeID
	fingerprint string

	onFound DiscoveryCallback
	known   map[string]time.Time
	ctx     context.Context
	cancel  context.CancelFunc
}

// NewDiscoverer creates a LAN discoverer.
func NewDiscoverer(nodeID types.NodeID, fingerprint string, port int) *Discoverer {
	ctx, cancel := context.WithCancel(context.Background())
	return &Discoverer{
		port:        port,
		nodeID:      nodeID,
		fingerprint: fingerprint,
		known:       make(map[string]time.Time),
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Start begins broadcasting and listening for peer announcements.
func (d *Discoverer) Start() error {
	go d.respondLoop()
	go d.listenLoop()
	log.Printf("[discovery] LAN discovery enabled on port %d via UDP multicast", d.port)
	return nil
}

// Stop shuts down the discoverer.
func (d *Discoverer) Stop() {
	d.cancel()
}

// SetCallback registers the callback invoked when a new peer is discovered.
func (d *Discoverer) SetCallback(fn DiscoveryCallback) {
	d.onFound = fn
}

// respondLoop periodically broadcasts our presence.
func (d *Discoverer) respondLoop() {
	conn, err := net.Dial("udp", mdnsAddr)
	if err != nil {
		log.Printf("[discovery] responder: %v", err)
		return
	}
	defer conn.Close()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	d.broadcast(conn)
	for {
		select {
		case <-ticker.C:
			d.broadcast(conn)
		case <-d.ctx.Done():
			return
		}
	}
}

// broadcast sends a JSON announcement packet.
func (d *Discoverer) broadcast(conn net.Conn) {
	host, _ := os.Hostname()
	payload := announcePayload{
		Service:     "sessionbridge",
		Version:     1,
		NodeID:      string(d.nodeID),
		Fingerprint: d.fingerprint,
		Port:        d.port,
		Hostname:    host,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	conn.Write(data)
}

// listenLoop listens for multicast announcements from other nodes.
func (d *Discoverer) listenLoop() {
	addr, err := net.ResolveUDPAddr("udp", mdnsAddr)
	if err != nil {
		log.Printf("[discovery] listener: %v", err)
		return
	}

	conn, err := net.ListenMulticastUDP("udp", nil, addr)
	if err != nil {
		log.Printf("[discovery] listener: %v (falling back to any:5353)", err)
		// Fallback: bind to port directly
		conn2, err2 := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 5353})
		if err2 != nil {
			log.Printf("[discovery] listener fallback also failed: %v", err2)
			return
		}
		conn = conn2
	}
	defer conn.Close()

	buf := make([]byte, 2048)
	for {
		select {
		case <-d.ctx.Done():
			return
		default:
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			n, remoteAddr, err := conn.ReadFromUDP(buf)
			if err != nil {
				continue
			}

			d.handlePacket(buf[:n], remoteAddr)
		}
	}
}

// handlePacket parses a JSON announcement and extracts peer info.
func (d *Discoverer) handlePacket(data []byte, remote *net.UDPAddr) {
	var payload announcePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return
	}

	// Validate it's a sessionbridge announcement
	if payload.Service != "sessionbridge" || payload.Version != 1 {
		return
	}

	// Skip ourselves
	if payload.NodeID == string(d.nodeID) {
		return
	}

	// Skip already-discovered
	if _, known := d.known[payload.NodeID]; known {
		return
	}
	d.known[payload.NodeID] = time.Now()

	// Resolve address: use packet source IP + announced port
	addr := net.JoinHostPort(remote.IP.String(), fmt.Sprintf("%d", payload.Port))

	peer := DiscoveredPeer{
		NodeID:      types.NodeID(payload.NodeID),
		Address:     addr,
		Fingerprint: payload.Fingerprint,
		Tags:        []string{"lan"},
	}

	log.Printf("[discovery] found peer %s at %s (fp=%s)", peer.NodeID, peer.Address, peer.Fingerprint)

	if d.onFound != nil {
		d.onFound(peer)
	}
}

// DiscoveredPeers returns all peers discovered so far.
func (d *Discoverer) DiscoveredPeers() []DiscoveredPeer {
	// Not implemented in this simple version — use callback
	return nil
}
