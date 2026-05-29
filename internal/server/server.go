package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/sessionnode/go-core/internal/dispatcher"
	"github.com/user/sessionnode/go-core/internal/mesh"
	"github.com/user/sessionnode/go-core/internal/process"
	"github.com/user/sessionnode/go-core/internal/session"
	"github.com/user/sessionnode/go-core/internal/wsconn"
	"github.com/user/sessionnode/go-core/pkg/protocol"
	"github.com/user/sessionnode/go-core/pkg/types"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:   4096,
	WriteBufferSize:  4096,
	HandshakeTimeout: 10 * time.Second,
	CheckOrigin:      func(r *http.Request) bool { return true },
}

// Server holds the HTTP server, dispatcher, and all shared state.
type Server struct {
	addr         string
	tlsCert      string
	tlsKey       string
	dispatcher   *dispatcher.Dispatcher
	sessions     *session.Store
	connRegistry *wsconn.Registry
	procManager  *process.Manager
	httpServer   *http.Server
	wg           sync.WaitGroup
	conns        map[*websocket.Conn]struct{}
	connsMu      sync.Mutex

	// Peer authentication — set server-side so peers cannot spoof actor type.
	identity   *mesh.NodeIdentity
	trustStore *mesh.TrustStore
	peerConns  map[string]string // connID → peerNodeID
	peerConnsMu sync.RWMutex

	// Control access token. When non-empty, /ws requires matching ?token= or
	// Authorization: Bearer header. This is the same SESSIONNODE_TOKEN value
	// used by the dispatcher's TokenAuthenticator.
	token string

	// Invite store for remote pairing via /peer/invite/accept.
	inviteStore *mesh.InviteStore
}

// New creates a Server. Call Start() to begin listening.
func New(addr string, d *dispatcher.Dispatcher, s *session.Store, cr *wsconn.Registry, pm *process.Manager, identity *mesh.NodeIdentity, trustStore *mesh.TrustStore, token string) *Server {
	return NewWithTLS(addr, "", "", d, s, cr, pm, identity, trustStore, token)
}

// NewWithTLS creates a Server with optional TLS. Leave certFile/keyFile empty for plain HTTP.
func NewWithTLS(addr, certFile, keyFile string, d *dispatcher.Dispatcher, s *session.Store, cr *wsconn.Registry, pm *process.Manager, identity *mesh.NodeIdentity, trustStore *mesh.TrustStore, token string) *Server {
	sv := &Server{
		addr:         addr,
		tlsCert:      certFile,
		tlsKey:       keyFile,
		dispatcher:   d,
		sessions:     s,
		connRegistry: cr,
		procManager:  pm,
		conns:        make(map[*websocket.Conn]struct{}),
		identity:     identity,
		trustStore:   trustStore,
		peerConns:    make(map[string]string),
		token:        token,
		inviteStore:  nil,
	}
	sv.registerHandlers()
	return sv
}

// SetInviteStore sets the invite store for remote pairing.
func (s *Server) SetInviteStore(is *mesh.InviteStore) {
	s.inviteStore = is
}

func (s *Server) registerHandlers() {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/info", s.handleInfo)
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/processes", s.handleProcesses)
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/peer/ws", s.handlePeerWS)
	mux.HandleFunc("/peer/invite/accept", s.handlePeerInviteAccept)
	s.httpServer = &http.Server{Addr: s.addr, Handler: mux}
}

// Start begins listening and handles graceful shutdown on SIGINT/SIGTERM.
// Uses TLS if cert and key files were configured via NewWithTLS, otherwise plain HTTP.
func (s *Server) Start() error {
	// Channel for shutdown signals
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-quit
		log.Printf("[server] received signal %v, shutting down...", sig)
		s.shutdown()
	}()

	scheme := "http"
	wsScheme := "ws"
	if s.tlsCert != "" && s.tlsKey != "" {
		scheme = "https"
		wsScheme = "wss"
	}

	log.Printf("[server] SessionNode Go Core listening on %s (%s)", s.addr, scheme)
	log.Printf("[server]   WS:   %s://%s/ws", wsScheme, s.addr)
	log.Printf("[server]   API:  %s://%s/health", scheme, s.addr)

	if s.tlsCert != "" && s.tlsKey != "" {
		if err := s.httpServer.ListenAndServeTLS(s.tlsCert, s.tlsKey); err != nil && err != http.ErrServerClosed {
			return err
		}
	} else {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return err
		}
	}
	return nil
}

// shutdown gracefully stops the server.
func (s *Server) shutdown() {
	log.Println("[server] closing WebSocket connections...")
	s.connsMu.Lock()
	for conn := range s.conns {
		conn.Close()
	}
	s.connsMu.Unlock()

	log.Println("[server] cleaning up processes...")
	s.procManager.Cleanup()

	log.Println("[server] cleaning up sessions...")
	s.sessions.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Println("[server] stopping HTTP server...")
	if err := s.httpServer.Shutdown(ctx); err != nil {
		log.Printf("[server] shutdown error: %v", err)
	}

	s.wg.Wait()
	log.Println("[server] shutdown complete")
}

// Handler exposes the HTTP handler for testing and embedding.
func (s *Server) Handler() http.Handler { return s.httpServer.Handler }

func (s *Server) addConn(conn *websocket.Conn) {
	s.connsMu.Lock()
	s.conns[conn] = struct{}{}
	s.connsMu.Unlock()
}

func (s *Server) removeConn(conn *websocket.Conn) {
	s.connsMu.Lock()
	delete(s.conns, conn)
	s.connsMu.Unlock()
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"time":   time.Now().UnixMilli(),
	})
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"name":         "SessionNode Go Core",
		"version":      "0.3.0",
		"description":  "Phase 1 — Real process execution + streaming + TLS",
		"sessionCount": s.sessions.Count(),
		"procCount":    s.procManager.Count(),
	})
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	sessions := s.sessions.List()
	out := make([]map[string]interface{}, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, map[string]interface{}{
			"sessionId": string(sess.ID),
			"pluginId":  string(sess.PluginID),
			"state":     sess.State,
			"command":   sess.Command,
			"cwd":       sess.Cwd,
			"createdAt": sess.CreatedAt,
		})
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"sessions": out,
		"total":    len(out),
	})
}

func (s *Server) handleProcesses(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	procs := s.procManager.List()
	out := make([]map[string]interface{}, 0, len(procs))
	for _, p := range procs {
		out = append(out, map[string]interface{}{
			"sessionId": string(p.SessionID),
			"pid":       p.PID,
			"state":     p.State,
			"exitCode":  p.ExitCode,
			"createdAt": p.CreatedAt,
		})
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"processes": out,
		"total":     len(out),
	})
}

// handleWS upgrades to WebSocket and manages a single client connection.
// When s.token is non-empty, the token must be provided via ?token= query param
// or Authorization: Bearer header before the WebSocket upgrade is accepted.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if s.token != "" {
		token := r.URL.Query().Get("token")
		if token == "" {
			// Fallback to Authorization Bearer header.
			if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
				token = strings.TrimPrefix(auth, "Bearer ")
			}
		}
		if token != s.token {
			log.Printf("[ws] token rejected from %s", r.RemoteAddr)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "missing or invalid token",
			})
			return
		}
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ws] upgrade error: %v", err)
		return
	}

	s.wg.Add(1)
	defer s.wg.Done()

	s.addConn(conn)
	defer s.removeConn(conn)

	// Write channel serializes all outgoing messages for this connection.
	writeCh := make(chan []byte, 128)
	var writeWg sync.WaitGroup
	writeWg.Add(1)
	go s.writeLoop(conn, writeCh, &writeWg)

	// Register this connection in the multi-subscriber registry.
	wsConn := s.connRegistry.RegisterConn(writeCh, types.Actor{
		Type: "web",
		ID:   r.RemoteAddr,
	})
	connID := wsConn.ID

	log.Printf("[ws] client connected from %s (conn=%s)", r.RemoteAddr, connID)

	conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		return nil
	})

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("[ws] read error: %v", err)
			}
			break
		}

		resp := s.handleMessage(raw, connID)
		if resp == nil {
			continue
		}

		respBytes, err := resp.MarshalJSON()
		if err != nil {
			log.Printf("[ws] marshal error: %v", err)
			continue
		}

		select {
		case writeCh <- respBytes:
		default:
			log.Printf("[ws] write channel full, dropping response")
		}
	}

	// Cleanup: remove this connection and all its subscriptions.
	// Using UnregisterConn (which removes by connection ID) instead of
	// per-session cleanup ensures that if a new WS connection has already
	// re-subscribed to the same sessions, the new subscriptions are not
	// affected by this cleanup.
	s.connRegistry.UnregisterConn(connID)

	close(writeCh)
	writeWg.Wait()
	conn.Close()
	log.Printf("[ws] client disconnected from %s", r.RemoteAddr)
}

// handlePeerWS upgrades to WebSocket and performs a peer handshake
// (challenge-response using ed25519) before accepting node-to-node messages.
func (s *Server) handlePeerWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[peer-ws] upgrade error: %v", err)
		return
	}

	s.wg.Add(1)
	defer s.wg.Done()

	s.addConn(conn)
	defer s.removeConn(conn)

	log.Printf("[peer-ws] new peer connection from %s", r.RemoteAddr)

	// Step 1: Read peer.hello with 30s timeout.
	helloMsg, err := s.readPeerMessage(conn, 30*time.Second)
	if err != nil {
		log.Printf("[peer-ws] handshake step 1 (hello) failed: %v", err)
		_ = s.writePeerError(conn, "", protocol.ErrCodePeerHandshakeFailed, "failed to read peer.hello: "+err.Error())
		return
	}
	if helloMsg.Type != protocol.MsgTypePeerHello {
		log.Printf("[peer-ws] expected peer.hello, got %q", helloMsg.Type)
		_ = s.writePeerError(conn, helloMsg.RequestID, protocol.ErrCodePeerHandshakeFailed, "expected peer.hello")
		return
	}

	peerNodeID := string(helloMsg.NodeID)

	// Decode peer.hello payload.
	var helloPayload struct {
		PublicKey   string `json:"publicKey"`
		Fingerprint string `json:"fingerprint"`
		Timestamp   int64  `json:"timestamp"`
	}
	if err := json.Unmarshal(helloMsg.Payload, &helloPayload); err != nil {
		log.Printf("[peer-ws] peer.hello payload decode: %v", err)
		_ = s.writePeerError(conn, "", protocol.ErrCodePeerHandshakeFailed, "invalid peer.hello payload")
		return
	}

	// Step 2: Validate peer against trust store.
	if s.trustStore == nil {
		log.Printf("[peer-ws] no trust store configured, rejecting peer %s", peerNodeID)
		_ = s.writePeerError(conn, "", protocol.ErrCodePeerUnknown, "no trust store configured")
		return
	}

	trustedPeer, err := s.trustStore.Get(peerNodeID)
	if err != nil {
		log.Printf("[peer-ws] unknown peer %s: %v", peerNodeID, err)
		_ = s.writePeerError(conn, "", protocol.ErrCodePeerUnknown, "peer not in trust store: "+peerNodeID)
		return
	}

	if trustedPeer.Status == mesh.TrustStatusRevoked {
		log.Printf("[peer-ws] revoked peer %s", peerNodeID)
		_ = s.writePeerError(conn, "", protocol.ErrCodePeerRevoked, "peer trust has been revoked: "+peerNodeID)
		return
	}

	if trustedPeer.Status == mesh.TrustStatusExpired {
		log.Printf("[peer-ws] expired trust for peer %s", peerNodeID)
		_ = s.writePeerError(conn, "", protocol.ErrCodePeerExpired, "peer trust has expired: "+peerNodeID)
		return
	}

	if trustedPeer.TrustExpiresAt > 0 && time.Now().UnixMilli() > trustedPeer.TrustExpiresAt {
		log.Printf("[peer-ws] expired trust for peer %s (by timestamp)", peerNodeID)
		_ = s.writePeerError(conn, "", protocol.ErrCodePeerExpired, "peer trust has expired: "+peerNodeID)
		return
	}

	// Decode the presented public key from the hello payload.
	peerPubKeyBytes, err := base64.StdEncoding.DecodeString(helloPayload.PublicKey)
	if err != nil {
		log.Printf("[peer-ws] invalid public key encoding from %s", peerNodeID)
		_ = s.writePeerError(conn, "", protocol.ErrCodePeerHandshakeFailed, "invalid public key encoding")
		return
	}

	// Verify public key matches trust store (compare raw bytes).
	if len(trustedPeer.PublicKey) != len(peerPubKeyBytes) {
		log.Printf("[peer-ws] public key length mismatch for %s", peerNodeID)
		_ = s.writePeerError(conn, "", protocol.ErrCodePeerKeyMismatch, "public key does not match trust store for: "+peerNodeID)
		return
	}
	for i := range trustedPeer.PublicKey {
		if trustedPeer.PublicKey[i] != peerPubKeyBytes[i] {
			log.Printf("[peer-ws] public key mismatch for %s", peerNodeID)
			_ = s.writePeerError(conn, "", protocol.ErrCodePeerKeyMismatch, "public key does not match trust store for: "+peerNodeID)
			return
		}
	}

	// Step 3: Generate challenge (32 random bytes, base64-encoded).
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		log.Printf("[peer-ws] failed to generate nonce: %v", err)
		_ = s.writePeerError(conn, "", protocol.ErrCodePeerHandshakeFailed, "internal error generating challenge")
		return
	}
	nonceB64 := base64.StdEncoding.EncodeToString(nonce)

	challengeID := types.RequestID(fmt.Sprintf("chal-%s-%d", peerNodeID, time.Now().UnixNano()))
	challengeMsg := protocol.NewPeerChallenge(challengeID, nonceB64)
	if err := s.writePeerMessage(conn, challengeMsg, 30*time.Second); err != nil {
		log.Printf("[peer-ws] failed to send challenge: %v", err)
		return
	}
	log.Printf("[peer-ws] challenge sent to %s (requestId=%s)", peerNodeID, challengeID)

	// Step 4: Read peer.response.
	respMsg, err := s.readPeerMessage(conn, 30*time.Second)
	if err != nil {
		log.Printf("[peer-ws] handshake step 3 (response) failed: %v", err)
		_ = s.writePeerError(conn, challengeID, protocol.ErrCodePeerHandshakeFailed, "failed to read peer.response: "+err.Error())
		return
	}
	if respMsg.Type != protocol.MsgTypePeerResponse {
		log.Printf("[peer-ws] expected peer.response, got %q", respMsg.Type)
		_ = s.writePeerError(conn, challengeID, protocol.ErrCodePeerHandshakeFailed, "expected peer.response")
		return
	}

	var respPayload struct {
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(respMsg.Payload, &respPayload); err != nil {
		log.Printf("[peer-ws] peer.response payload decode: %v", err)
		_ = s.writePeerError(conn, challengeID, protocol.ErrCodePeerHandshakeFailed, "invalid peer.response payload")
		return
	}

	sigBytes, err := base64.StdEncoding.DecodeString(respPayload.Signature)
	if err != nil {
		log.Printf("[peer-ws] invalid signature encoding from %s", peerNodeID)
		_ = s.writePeerError(conn, challengeID, protocol.ErrCodePeerHandshakeFailed, "invalid signature encoding")
		return
	}

	// Step 5: Verify signature against trusted public key.
	if !ed25519.Verify(ed25519.PublicKey(peerPubKeyBytes), nonce, sigBytes) {
		log.Printf("[peer-ws] signature verification failed for %s", peerNodeID)
		_ = s.writePeerError(conn, challengeID, protocol.ErrCodePeerHandshakeFailed, "signature verification failed")
		return
	}

	// Step 6: Send peer.welcome.
	var serverNodeID types.NodeID
	if s.identity != nil {
		serverNodeID = types.NodeID(s.identity.NodeID)
	}
	welcomeMsg := protocol.NewPeerWelcome(serverNodeID)
	if err := s.writePeerMessage(conn, welcomeMsg, 30*time.Second); err != nil {
		log.Printf("[peer-ws] failed to send welcome: %v", err)
		return
	}

	log.Printf("[peer-ws] handshake complete for peer %s", peerNodeID)

	// Step 7: Register as peer connection (server-side tracking).
	writeCh := make(chan []byte, 128)
	var writeWg sync.WaitGroup
	writeWg.Add(1)
	go s.writeLoop(conn, writeCh, &writeWg)

	wsConn := s.connRegistry.RegisterConn(writeCh, types.Actor{
		Type: "node",
		ID:   peerNodeID,
	})
	connID := wsConn.ID

	s.peerConnsMu.Lock()
	s.peerConns[connID] = peerNodeID
	s.peerConnsMu.Unlock()

	// Update last seen in trust store.
	s.trustStore.UpdateLastSeen(peerNodeID)

	// Step 8: Read loop — all messages from this peer are trusted as node-to-node.
	conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		return nil
	})

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("[peer-ws] read error from %s: %v", peerNodeID, err)
			}
			break
		}

		resp := s.handleMessage(raw, connID)
		if resp == nil {
			continue
		}

		respBytes, err := resp.MarshalJSON()
		if err != nil {
			log.Printf("[peer-ws] marshal error: %v", err)
			continue
		}

		select {
		case writeCh <- respBytes:
		default:
			log.Printf("[peer-ws] write channel full, dropping response to %s", peerNodeID)
		}
	}

	// Cleanup.
	s.peerConnsMu.Lock()
	delete(s.peerConns, connID)
	s.peerConnsMu.Unlock()

	s.connRegistry.UnregisterConn(connID)
	close(writeCh)
	writeWg.Wait()
	conn.Close()
	log.Printf("[peer-ws] peer %s disconnected", peerNodeID)
}


// handlePeerInviteAccept handles HTTP POST requests for remote peer pairing.
// The caller POSTs its own identity (nodeId, publicKey, fingerprint) plus an
// optional addressHint.  We validate the one-time invite code, store the caller
// as a trusted peer, and return our identity so the caller can do the same.
func (s *Server) handlePeerInviteAccept(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	if s.identity == nil || s.trustStore == nil {
		http.Error(w, `{"error":"server identity or trust store not configured"}`, http.StatusInternalServerError)
		return
	}
	if s.inviteStore == nil {
		http.Error(w, `{"error":"invite store not configured"}`, http.StatusInternalServerError)
		return
	}

	var req struct {
		Code        string `json:"code"`
		NodeID      string `json:"nodeId"`
		PublicKey   string `json:"publicKey"`
		Fingerprint string `json:"fingerprint"`
		NameHint    string `json:"nameHint,omitempty"`
		AddressHint string `json:"addressHint,omitempty"`
		Address     string `json:"address,omitempty"` // legacy field name from older clients
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if req.Code == "" || req.NodeID == "" || req.PublicKey == "" || req.Fingerprint == "" {
		http.Error(w, `{"error":"code, nodeId, publicKey, and fingerprint are required"}`, http.StatusBadRequest)
		return
	}

	// Reject attempts to pair with self — a node must never be its own peer.
	if s.identity != nil && req.NodeID == s.identity.NodeID {
		log.Printf("[peer-invite] rejecting self-pairing attempt from %s", r.RemoteAddr)
		http.Error(w, `{"error":"cannot pair with yourself"}`, http.StatusBadRequest)
		return
	}

	invite, err := s.inviteStore.Consume(req.Code)
	if err != nil {
		log.Printf("[peer-invite] invalid code from %s: %v", r.RemoteAddr, err)
		http.Error(w, `{"error":"invalid or expired invite code"}`, http.StatusForbidden)
		return
	}

	pubKeyBytes, err := hex.DecodeString(req.PublicKey)
	if err != nil {
		http.Error(w, `{"error":"invalid public key encoding"}`, http.StatusBadRequest)
		return
	}
	if len(pubKeyBytes) != ed25519.PublicKeySize {
		http.Error(w, `{"error":"invalid public key length"}`, http.StatusBadRequest)
		return
	}

	var trustExpiresAt int64
	if invite.TrustDurationSeconds > 0 {
		trustExpiresAt = time.Now().UnixMilli() + (invite.TrustDurationSeconds * 1000)
	}

	peerName := req.NameHint
	if peerName == "" {
		peerName = req.NodeID
	}

	// Prefer addressHint; fall back to legacy "address" field; finally use the
	// TCP remote address so we have at least one way to reach the peer.
	addresses := []string{}
	addrHint := req.AddressHint
	if addrHint == "" {
		addrHint = req.Address
	}
	if addrHint != "" {
		addresses = append(addresses, addrHint)
	} else if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && host != "" {
		// Best-effort: assume the peer listens on the same host with the
		// default peer WebSocket path.  The caller can update this later.
		addresses = append(addresses, host+":9090")
	}

	peer := &mesh.TrustedPeer{
		NodeID:         req.NodeID,
		Name:           peerName,
		PublicKey:      pubKeyBytes,
		Fingerprint:    req.Fingerprint,
		Addresses:      addresses,
		TrustExpiresAt: trustExpiresAt,
		AutoReconnect:  true,
		Status:         mesh.TrustStatusOffline,
		LastSeen:       time.Now().UnixMilli(),
		Policy:         mesh.TrustPolicy{Mode: "full"},
	}

	if err := s.trustStore.Add(peer); err != nil {
		log.Printf("[peer-invite] failed to store peer: %v", err)
		http.Error(w, `{"error":"failed to store peer"}`, http.StatusInternalServerError)
		return
	}

	log.Printf("[peer-invite] accepted pairing from %s (%s)", req.NodeID, req.Fingerprint)

	resp := map[string]interface{}{
		"status": "accepted",
		"node": map[string]interface{}{
			"nodeId":      s.identity.NodeID,
			"publicKey":   hex.EncodeToString(s.identity.PublicKey),
			"fingerprint": s.identity.Fingerprint,
		},
		"trustExpiresAt": trustExpiresAt,
		"peerWsPath":     "/peer/ws",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// readPeerMessage reads a single message from the peer with a timeout.
func (s *Server) readPeerMessage(conn *websocket.Conn, timeout time.Duration) (*protocol.Message, error) {
	conn.SetReadDeadline(time.Now().Add(timeout))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	return protocol.UnmarshalMessage(raw)
}

// writePeerMessage writes a single message to the peer with a timeout.
func (s *Server) writePeerMessage(conn *websocket.Conn, msg *protocol.Message, timeout time.Duration) error {
	conn.SetWriteDeadline(time.Now().Add(timeout))
	data, err := msg.MarshalJSON()
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}

// writePeerError sends a peer.error message to the peer.
func (s *Server) writePeerError(conn *websocket.Conn, requestID types.RequestID, code, message string) error {
	return s.writePeerMessage(conn, protocol.NewPeerError(requestID, code, message), 5*time.Second)
}

// writeLoop reads from the channel and writes to the WebSocket connection.
func (s *Server) writeLoop(conn *websocket.Conn, ch <-chan []byte, wg *sync.WaitGroup) {
	defer wg.Done()
	for data := range ch {
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Printf("[ws] write error: %v", err)
			return
		}
	}
}

func (s *Server) handleMessage(raw []byte, connID string) *protocol.Message {
	msg, err := protocol.UnmarshalMessage(raw)
	if err != nil {
		log.Printf("[server] unmarshal error: %v", err)
		return protocol.NewError("", protocol.ErrCodeInvalidRequest, "invalid JSON")
	}

	switch {
	case msg.Type == protocol.MsgTypePing:
		return protocol.NewPong()
	case msg.Type == protocol.MsgTypePong:
		return nil
	case msg.Type == protocol.MsgTypeHello:
		return protocol.NewWelcome(msg.NodeID)
	default:
		return s.dispatchAction(msg, connID)
	}
}

func (s *Server) dispatchAction(msg *protocol.Message, connID string) *protocol.Message {
	capability := msg.Capability
	if capability == "" {
		capability = msg.Type
	}

	// Determine actor type based on connection identity (server-side, not client-claimed).
	actorType := "web"
	actorID := msg.ActorID

	// Check if this is a peer connection (authenticated via handlePeerWS handshake).
	s.peerConnsMu.RLock()
	peerNodeID, isPeer := s.peerConns[connID]
	s.peerConnsMu.RUnlock()

	if isPeer {
		// Peer connections are trusted — set actor type server-side.
		actorType = "node"
		actorID = peerNodeID
	} else {
		// Control connections: if we have a trust store (authenticated mode),
		// block clients that try to claim actorType=node. In dev/backward-compat
		// mode (no trust store), allow the old behavior.
		if msg.ActorType == "node" && s.trustStore != nil {
			return protocol.NewError(msg.RequestID, protocol.ErrCodeActorTypeNodeBlocked,
				"actorType=node is not allowed on control WS. Use /peer/ws for node-to-node connections.")
		}
		// Use client-provided actor type (defaults to "web").
		if msg.ActorType != "" {
			actorType = msg.ActorType
		}
	}

	// For control WS connections (not peers), the WS upgrade handler already
	// validated the token against s.token. Auto-populate the actor token so
	// dispatch-level auth doesn't require each message to carry it redundantly.
	if !isPeer && s.token != "" && msg.ActorToken == "" {
		msg.ActorToken = s.token
	}

	if actorID == "" {
		actorID = string(msg.NodeID)
	}
	req := &types.CapabilityRequest{
		RequestID:    msg.RequestID,
		PluginID:     types.PluginID(msg.PluginID),
		Capability:   capability,
		TargetNodeID: msg.TargetNodeID,
		Payload:      msg.Payload,
		Timestamp:    msg.Timestamp,
		ConnID:       connID,
		Actor: types.Actor{
			Type:  actorType,
			ID:    actorID,
			Token: msg.ActorToken,
		},
	}

	// For cross-node stream.subscribe, register the subscription locally
	// so that forwarded stream.chunk messages can reach the client.
	if capability == "stream.subscribe" && req.TargetNodeID != "" {
		s.registerLocalStreamSub(connID, req)
	}

	resp := s.dispatcher.Dispatch(req)
	return actionResponseToMessage(msg, resp)
}

// registerLocalStreamSub parses a stream.subscribe payload and registers a
// local subscription. This is needed for cross-node subscriptions where the
// dispatcher forwards the request to the remote node without calling the
// local executor.
func (s *Server) registerLocalStreamSub(connID string, req *types.CapabilityRequest) {
	var p struct {
		SessionID  string         `json:"sessionId"`
		Stream     string         `json:"stream"`
		StreamType string         `json:"streamType"`
		FromSeq    types.EventSeq `json:"fromSeq,omitempty"`
	}
	if err := json.Unmarshal(req.Payload, &p); err != nil || p.SessionID == "" {
		return
	}
	stream := p.Stream
	if p.StreamType != "" {
		stream = p.StreamType
	}
	if stream == "" {
		return
	}
	streamTypes := strings.Split(stream, ",")
	for i := range streamTypes {
		streamTypes[i] = strings.TrimSpace(streamTypes[i])
	}
	s.connRegistry.Subscribe(connID, types.SessionID(p.SessionID), streamTypes, req.PluginID, req.Actor, p.FromSeq)
}

func actionResponseToMessage(reqMsg *protocol.Message, resp *types.CapabilityResponse) *protocol.Message {
	out := &protocol.Message{
		Type:        protocol.MsgTypeActionResponse,
		RequestID:   resp.RequestID,
		OK:          resp.OK,
		RespondedBy: "",
		Timestamp:   time.Now().UnixMilli(),
	}
	if resp.Error != nil {
		out.Error = &types.CoreError{Code: resp.Error.Code, Message: resp.Error.Message}
	}
	if resp.Payload != nil {
		data, err := json.Marshal(resp.Payload)
		if err == nil {
			out.Payload = data
		}
	}
	return out
}

// extractSessionID attempts to parse a "sessionId" field from a JSON payload.
func extractSessionID(payload json.RawMessage) types.SessionID {
	var m map[string]interface{}
	if err := json.Unmarshal(payload, &m); err != nil {
		return ""
	}
	sid, ok := m["sessionId"].(string)
	if !ok || sid == "" {
		return ""
	}
	return types.SessionID(sid)
}
