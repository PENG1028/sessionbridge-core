package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/sessionnode/go-core/internal/dispatcher"
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
}

// New creates a Server. Call Start() to begin listening.
func New(addr string, d *dispatcher.Dispatcher, s *session.Store, cr *wsconn.Registry, pm *process.Manager) *Server {
	return NewWithTLS(addr, "", "", d, s, cr, pm)
}

// NewWithTLS creates a Server with optional TLS. Leave certFile/keyFile empty for plain HTTP.
func NewWithTLS(addr, certFile, keyFile string, d *dispatcher.Dispatcher, s *session.Store, cr *wsconn.Registry, pm *process.Manager) *Server {
	sv := &Server{
		addr:         addr,
		tlsCert:      certFile,
		tlsKey:       keyFile,
		dispatcher:   d,
		sessions:     s,
		connRegistry: cr,
		procManager:  pm,
		conns:        make(map[*websocket.Conn]struct{}),
	}
	sv.registerHandlers()
	return sv
}

func (s *Server) registerHandlers() {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/info", s.handleInfo)
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/processes", s.handleProcesses)
	mux.HandleFunc("/ws", s.handleWS)
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
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
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

	actorType := msg.ActorType
	if actorType == "" {
		actorType = "web"
	}
	actorID := msg.ActorID
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
