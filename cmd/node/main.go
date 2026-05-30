package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/user/sessionnode/go-core/internal/auth"
	"github.com/user/sessionnode/go-core/internal/config"
	"github.com/user/sessionnode/go-core/internal/dispatcher"
	"github.com/user/sessionnode/go-core/internal/executor"
	"github.com/user/sessionnode/go-core/internal/history"
	"github.com/user/sessionnode/go-core/internal/logs"
	"github.com/user/sessionnode/go-core/internal/mesh"
	"github.com/user/sessionnode/go-core/internal/notify"
	"github.com/user/sessionnode/go-core/internal/permission"
	"github.com/user/sessionnode/go-core/internal/plan"
	"github.com/user/sessionnode/go-core/internal/pluginmanifest"
	"github.com/user/sessionnode/go-core/internal/process"
	"github.com/user/sessionnode/go-core/internal/run"
	"github.com/user/sessionnode/go-core/internal/server"
	"github.com/user/sessionnode/go-core/internal/session"
	"github.com/user/sessionnode/go-core/internal/topology"
	"github.com/user/sessionnode/go-core/internal/wsconn"
	"github.com/user/sessionnode/go-core/pkg/protocol"
	"github.com/user/sessionnode/go-core/pkg/types"
)

func main() {
	tlsCert := os.Getenv("SESSIONNODE_TLS_CERT")
	tlsKey := os.Getenv("SESSIONNODE_TLS_KEY")
	nodeID := types.NodeID(getEnv("NODE_ID", "node_local"))
	// Config — load from file or create defaults.
	cfgPath := os.Getenv("SESSIONNODE_CONFIG")
	if cfgPath == "" {
		home, _ := os.UserHomeDir()
		cfgPath = filepath.Join(home, ".sessionnode", "config.json")
	}
	cfgMgr := config.NewManager(cfgPath)
	if err := cfgMgr.Load(); err != nil {
		log.Fatalf("config load: %v", err)
	}
	cfg := cfgMgr.Get()

	// Change to user's home directory so os.Getwd() returns a sensible
	// default regardless of where the process was launched.
	if home, err := os.UserHomeDir(); err == nil {
		if err := os.Chdir(home); err != nil {
			log.Printf("[startup] chdir to home: %v (continuing)", err)
		}
	}

	// Token resolution: env → config file → auto-generate
	token := os.Getenv("SESSIONNODE_TOKEN")
	if token == "" && cfg.Core.Auth.AdminToken != "" {
		token = cfg.Core.Auth.AdminToken
		log.Printf("[startup] using token from config file")
	}
	if token == "" && os.Getenv("SESSIONNODE_ALLOW_INSECURE") != "1" {
		token = generateToken()
		log.Printf("[startup] auto-generated token (saved to %s)", cfgPath)
		cfgMgr.Set("core.auth.adminToken", token)
	}

	addr := getEnv("LISTEN_ADDR", cfg.Core.ListenAddr)

	// Data directory — used by mesh, logging, and other subsystems.
	logDir := getEnv("SESSIONNODE_DATA_DIR", cfg.Core.DataDir)

	// Mesh: node identity and trust store for peer authentication.
	nodeIdentity, err := mesh.LoadOrCreateIdentity(logDir, string(nodeID))
	if err != nil {
		log.Fatalf("mesh identity: %v", err)
	}
	// If nodeID was the default sentinel, adopt the deterministic ID from identity.
	if nodeID == "node_local" {
		nodeID = types.NodeID(nodeIdentity.NodeID)
	}
	log.Printf("[startup] node identity: nodeId=%s fingerprint=%s", nodeIdentity.NodeID, nodeIdentity.Fingerprint)

	trustPath := filepath.Join(logDir, "trusted_peers.json")
	trustStore := mesh.NewTrustStore(trustPath)
	if err := trustStore.Load(); err != nil {
		log.Printf("[startup] trust store load: %v (starting with empty store)", err)
	}
	trustStore.SetLocalNodeID(string(nodeID))
	log.Printf("[startup] trust store: %d trusted peer(s)", len(trustStore.List()))

	// Logging — structured rotating logs.
	_, auditLogger, err := logs.Setup(logDir, cfg.Core.Log.Level)
	if err != nil {
		log.Fatalf("logs.Setup: %v", err)
	}
	defer auditLogger.Close()

	// In-memory observability stores for live query via logs.tail / audit.list.
	logBuffer := logs.NewBuffer(5000)
	auditStore := logs.NewAuditStore()

	audit := &dispatchAuditBridge{inner: auditLogger, store: auditStore}

	log.Printf("[startup] starting sessionnode go-core — node=%s listen=%s dataDir=%s", nodeID, addr, logDir)

	// Session store
	sessStore := session.NewStore()

	// Connection registry (routes process output to WebSocket clients)
	connRegistry := wsconn.NewRegistry()

	// Process manager (real OS process execution with output push)
	procManager := process.NewManager(connRegistry.PushChunk, connRegistry.PushSessionEvent)

	// History store — session retention and replay
	historyStore := history.New("")
	defer historyStore.Cleanup()
	wrappedPush := func(sid types.SessionID, streamType string, seq types.EventSeq, data string) {
		historyStore.Record(sid, streamType, seq, data)
		connRegistry.PushChunk(sid, streamType, seq, data)
	}
	wrappedEvent := func(sid types.SessionID, seq types.EventSeq, eventType string, data interface{}) {
		if eventType == "started" || eventType == "exited" {
			historyStore.RecordEvent(sid, seq, "session."+eventType, data)
		}
		connRegistry.PushSessionEvent(sid, seq, eventType, data)
	}
	procManager = process.NewManager(wrappedPush, wrappedEvent)
	procManager.SetOnSpawn(func(sid types.SessionID) {
		historyStore.InitSession(sid, types.DefaultHistoryPolicy())
	})

	// Notifier — broadcasts notifications/approvals to all WebSocket clients
	notifyMgr := notify.NewManager(connRegistry.Broadcast)

	// Topology — peer-to-peer connections and request forwarding
	peers := make([]topology.PeerConfig, len(cfg.Topology.Peers))
	for i, p := range cfg.Topology.Peers {
		peers[i] = topology.PeerConfig{ID: types.NodeID(p.ID), Address: p.Address, Tags: p.Tags}
	}
	topoCfg := topology.Config{
		LocalID:   nodeID,
		LocalName: cfg.Node.Name,
		Identity:  nodeIdentity,
		Peers:     peers,
	}
	topo := topology.New(topoCfg)
	// Forward stream chunks and session events from peers to local subscribers.
	topo.SetStreamChunkHandler(func(msg *protocol.Message) {
		switch msg.Type {
		case protocol.MsgTypeStreamChunk:
			connRegistry.PushChunk(msg.SessionID, msg.StreamType, msg.EventSeq, msg.Data)
		case protocol.MsgTypeSessionEvent:
			connRegistry.PushSessionEvent(msg.SessionID, msg.EventSeq, msg.Data, msg.Payload)
		}
	})
	// Restore persistent peers from trust store — peers added via invite
	// pairing that should automatically reconnect after a Core restart.
	for _, tp := range trustStore.List() {
		if tp.Status == mesh.TrustStatusRevoked || tp.Status == mesh.TrustStatusExpired {
			continue
		}
		if !tp.AutoReconnect {
			continue
		}
		if len(tp.Addresses) == 0 {
			continue
		}
		if err := topo.AddOrUpdatePeer(types.NodeID(tp.NodeID), tp.Addresses[0], true); err != nil {
			log.Printf("[startup] restore peer %s: %v", tp.NodeID, err)
		}
	}
	topoCtx, topoCancel := context.WithCancel(context.Background())
	go topo.Start(topoCtx)
	defer topoCancel()

	// Plugin registry — discover manifests from disk.
	// Start with configured dirs (or defaults), then always layer on
	// SESSIONNODE_PLUGIN_DIRS (additive, not a fallback), then check for
	// a local ./plugins/ directory (development mode).
	pluginDirs := pluginmanifest.ScanDirs(cfg.Plugin.PluginDirs)
	if env := os.Getenv("SESSIONNODE_PLUGIN_DIRS"); env != "" {
		for _, dir := range strings.Split(env, string(os.PathListSeparator)) {
			if trimmed := strings.TrimSpace(dir); trimmed != "" {
				pluginDirs = append(pluginDirs, trimmed)
			}
		}
	}
	if localPlugins := filepath.Join(".", "plugins"); fileExists(localPlugins) {
		pluginDirs = append(pluginDirs, localPlugins)
	}
	manifestReg := pluginmanifest.NewPluginRegistry(
		pluginDirs,
		cfg.Plugin.DisabledPlugins,
	)
	log.Printf("[startup] discovered %d plugin(s) from %d dir(s)",
		len(manifestReg.ListPlugins()), len(pluginDirs))

	// Build capability map from manifests, merging with hardcoded fallback.
	caps := mergeCapMaps(
		manifestReg.CapabilityMap(),
		capMapFromAllPluginsCaps(permission.AllPluginsCaps),
	)

	// Permission checker.
	permCaps := make(map[types.PluginID][]string, len(caps))
	for pidStr, list := range caps {
		permCaps[types.PluginID(pidStr)] = list
	}
	permChecker := permission.NewChecker(
		permission.NewMapRegistry(permCaps),
		permission.NewAllowAllPolicy(permCaps),
	)

	// Authenticator
	authenticator := auth.NewTokenAuthenticator(token)

	// Plugin registry for the dispatcher — built from manifest discovery + core.
	dispPlugins := newDispPluginRegistry(manifestReg)

	// Run store — long-lived resource index
	runStore := run.NewStore()

	// Executor registry
	execDeps := &executor.Deps{
		Sessions:   sessStore,
		Processes:  procManager,
		ConnRoutes: connRegistry,
		Notifier:   notifyMgr,
		Config:     cfgMgr,
		Nodes:      topo,
		Topology:   topo,
		History:    historyStore,
		Manifests:  manifestReg,
		RunStore:   runStore,
		Mesh:       &mesh.MeshState{Identity: nodeIdentity, TrustStore: trustStore, InviteStore: mesh.NewInviteStore()},
		LogBuffer:  logBuffer,
		AuditStore: auditStore,
	}
	execReg := executor.New(execDeps)

	// Dispatcher
	d := dispatcher.New(
		authenticator,
		dispPlugins,
		permChecker,
		plan.NewManager(plan.NewPlanStore(), plan.DefaultHighRiskCaps), /* planner */
		execReg,
		audit,
		topo,
		nodeID,
	)
	topo.SetDispatcher(d)

	// Server (with optional TLS)
	var sv *server.Server
	if tlsCert != "" && tlsKey != "" {
		sv = server.NewWithTLS(addr, tlsCert, tlsKey, d, sessStore, connRegistry, procManager, nodeIdentity, trustStore, token)
		sv.SetInviteStore(execDeps.Mesh.InviteStore)
	} else {
		sv = server.New(addr, d, sessStore, connRegistry, procManager, nodeIdentity, trustStore, token)
		sv.SetInviteStore(execDeps.Mesh.InviteStore)
	}
	sv.SetTopology(topo)
	topo.SetAuthToken(token)

	fmt.Printf("SessionNode Go Core — Phase 1\n")
	fmt.Printf("  Node ID: %s\n", nodeID)
	fmt.Printf("  Listen:  %s\n", addr)
	fmt.Printf("  Config:  %s\n", cfgPath)
	fmt.Printf("  Logs:    %s\n", filepath.Join(logDir, "logs"))
	fmt.Printf("  Token:   %s\n", map[bool]string{true: "enabled", false: "disabled (dev mode)"}[token != ""])

	// Fail or warn if listening on a public address without a token.
	validatePublicAccess(addr, token)

	if err := sv.Start(); err != nil {
		log.Fatalf("server error: %v", err)
	}
	log.Println("[startup] server stopped cleanly")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// isPublicAddr returns true when the listen address is not localhost/loopback.
func isPublicAddr(addr string) bool {
	if addr == "" {
		return false
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && !ip.IsLoopback() {
		return true
	}
	return false
}

// generateToken creates a 32-byte random hex token and logs it.
func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("failed to generate random token: %v", err)
	}
	return hex.EncodeToString(b)
}

// validatePublicAccess fails fast when listening on a public address without a token.
// Set SESSIONNODE_ALLOW_INSECURE=1 to run without a token on a public address.
func validatePublicAccess(addr, token string) {
	if token != "" {
		return
	}
	allowInsecure := os.Getenv("SESSIONNODE_ALLOW_INSECURE") == "1"
	if allowInsecure {
		log.Printf("[startup] public access allowed: LISTEN_ADDR=%s, SESSIONNODE_ALLOW_INSECURE=1", addr)
		return
	}
	if isPublicAddr(addr) {
		log.Fatalf("FATAL: LISTEN_ADDR=%s with empty SESSIONNODE_TOKEN is not allowed. Set SESSIONNODE_TOKEN or SESSIONNODE_ALLOW_INSECURE=1", addr)
	}
}

// fileExists returns true if the given path exists and is a directory.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// --- Simple implementations ---

// dispPluginRegistry implements dispatcher.PluginRegistry from the
// production PluginRegistry (plus the built-in core plugin).
type dispPluginRegistry struct {
	entries map[types.PluginID]*dispatcher.PluginEntry
}

func newDispPluginRegistry(reg *pluginmanifest.PluginRegistry) *dispPluginRegistry {
	r := &dispPluginRegistry{
		entries: make(map[types.PluginID]*dispatcher.PluginEntry),
	}
	// Built-in core plugin is always present.
	r.entries["sessionnode-core"] = &dispatcher.PluginEntry{ID: "sessionnode-core", Enabled: true}
	// Discovered plugins.
	for _, s := range reg.ListPlugins() {
		r.entries[types.PluginID(s.ID)] = &dispatcher.PluginEntry{ID: types.PluginID(s.ID), Enabled: s.Enabled}
	}
	return r
}

func (r *dispPluginRegistry) Get(id types.PluginID) (*dispatcher.PluginEntry, error) {
	p, ok := r.entries[id]
	if !ok {
		return nil, fmt.Errorf("plugin not found: %s", id)
	}
	return p, nil
}

// mergeCapMaps merges b into a (a takes priority on duplicate keys).
func mergeCapMaps(a, b map[string][]string) map[string][]string {
	out := make(map[string][]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if _, exists := out[k]; !exists {
			out[k] = v
		}
	}
	return out
}

// capMapFromAllPluginsCaps converts permission.AllPluginsCaps (which uses
// typed PluginID keys) to a plain string map for merging with manifest data.
func capMapFromAllPluginsCaps(src map[types.PluginID][]string) map[string][]string {
	out := make(map[string][]string, len(src))
	for pid, list := range src {
		out[string(pid)] = list
	}
	return out
}

// dispatchAuditBridge converts dispatcher.AuditLogger calls to structured
// logs.AuditEntry records (file) and logs.AuditRecord (in-memory).
type dispatchAuditBridge struct {
	inner *logs.AuditLogger
	store *logs.AuditStore
}

func (b *dispatchAuditBridge) Log(req *types.CapabilityRequest, allowed bool, detail string) {
	entry := logs.AuditEntry{
		PluginID:   string(req.PluginID),
		ActorType:  req.Actor.Type,
		ActorID:    req.Actor.ID,
		Capability: req.Capability,
		TargetNode: string(req.TargetNodeID),
		Allowed:    allowed,
		Detail:     detail,
		RequestID:  string(req.RequestID),
	}
	b.inner.Log(entry)

	// Also record to in-memory store for audit.list queries.
	outcome := "ok"
	if !allowed {
		outcome = "denied"
	}
	if b.store != nil {
		b.store.Record(logs.AuditRecord{
			Timestamp: entry.Timestamp,
			EventType: "capability.call",
			Actor:     entry.ActorType + ":" + entry.ActorID,
			Target:    entry.PluginID + "/" + entry.Capability,
			Outcome:   outcome,
			Metadata: map[string]interface{}{
				"requestId":  entry.RequestID,
				"targetNode": entry.TargetNode,
				"detail":     detail,
			},
		})
	}

	if !allowed {
		log.Printf("[AUDIT] DENY  %s.%s by %s/%s — %s", req.PluginID, req.Capability, req.Actor.Type, req.Actor.ID, detail)
	}
}
