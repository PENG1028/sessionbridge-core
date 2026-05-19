package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/user/sessionnode/go-core/internal/auth"
	"github.com/user/sessionnode/go-core/internal/config"
	"github.com/user/sessionnode/go-core/internal/dispatcher"
	"github.com/user/sessionnode/go-core/internal/executor"
	"github.com/user/sessionnode/go-core/internal/logs"
	"github.com/user/sessionnode/go-core/internal/notify"
	"github.com/user/sessionnode/go-core/internal/permission"
	"github.com/user/sessionnode/go-core/internal/process"
	"github.com/user/sessionnode/go-core/internal/server"
	"github.com/user/sessionnode/go-core/internal/session"
	"github.com/user/sessionnode/go-core/internal/topology"
	"github.com/user/sessionnode/go-core/internal/wsconn"
	"github.com/user/sessionnode/go-core/pkg/types"
)

func main() {
	tlsCert := os.Getenv("SESSIONNODE_TLS_CERT")
	tlsKey := os.Getenv("SESSIONNODE_TLS_KEY")
	nodeID := types.NodeID(getEnv("NODE_ID", "node_local"))
	token := os.Getenv("SESSIONNODE_TOKEN")

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

	addr := getEnv("LISTEN_ADDR", cfg.Core.ListenAddr)

	// Logging — structured rotating logs.
	logDir := getEnv("SESSIONNODE_DATA_DIR", cfg.Core.DataDir)
	_, auditLogger, err := logs.Setup(logDir, cfg.Core.Log.Level)
	if err != nil {
		log.Fatalf("logs.Setup: %v", err)
	}
	defer auditLogger.Close()

	audit := &dispatchAuditBridge{inner: auditLogger}

	log.Printf("[startup] starting sessionnode go-core — node=%s listen=%s dataDir=%s", nodeID, addr, logDir)

	// Session store
	sessStore := session.NewStore()

	// Connection registry (routes process output to WebSocket clients)
	connRegistry := wsconn.NewRegistry()

	// Process manager (real OS process execution with output push)
	procManager := process.NewManager(connRegistry.PushChunk, connRegistry.PushSessionEvent)

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
		Peers:     peers,
	}
	topo := topology.New(topoCfg)
	topoCtx, topoCancel := context.WithCancel(context.Background())
	go topo.Start(topoCtx)
	defer topoCancel()

	// Executor registry
	execDeps := &executor.Deps{
		Sessions:   sessStore,
		Processes:  procManager,
		ConnRoutes: connRegistry,
		Notifier:   notifyMgr,
		Config:     cfgMgr,
		Nodes:      topo,
	}
	execReg := executor.New(execDeps)

	// Permission checker — permissive by default
	permChecker := permission.NewChecker(
		&permissiveCapRegistry{},
		&permissivePolicyStore{},
	)

	// Authenticator
	authenticator := auth.NewTokenAuthenticator(token)

	// Plugin registry — allow all known plugins
	pluginReg := &simplePluginRegistry{
		plugins: map[types.PluginID]*dispatcher.PluginEntry{
			"shell":            {ID: "shell", Enabled: true},
			"sessionnode-core": {ID: "sessionnode-core", Enabled: true},
			"file-explorer":    {ID: "file-explorer", Enabled: true},
			"session":          {ID: "session", Enabled: true},
		},
	}

	// Dispatcher
	d := dispatcher.New(
		authenticator,
		pluginReg,
		permChecker,
		execReg,
		audit,
		topo,
		nodeID,
	)

	// Server (with optional TLS)
	var sv *server.Server
	if tlsCert != "" && tlsKey != "" {
		sv = server.NewWithTLS(addr, tlsCert, tlsKey, d, sessStore, connRegistry, procManager)
	} else {
		sv = server.New(addr, d, sessStore, connRegistry, procManager)
	}

	fmt.Printf("SessionNode Go Core — Phase 1\n")
	fmt.Printf("  Node ID: %s\n", nodeID)
	fmt.Printf("  Listen:  %s\n", addr)
	fmt.Printf("  Config:  %s\n", cfgPath)
	fmt.Printf("  Logs:    %s\n", filepath.Join(logDir, "logs"))
	fmt.Printf("  Token:   %s\n", map[bool]string{true: "enabled", false: "disabled (dev mode)"}[token != ""])

	log.Fatal(sv.Start())
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// --- Simple implementations ---

type simplePluginRegistry struct {
	plugins map[types.PluginID]*dispatcher.PluginEntry
}

func (r *simplePluginRegistry) Get(id types.PluginID) (*dispatcher.PluginEntry, error) {
	p, ok := r.plugins[id]
	if !ok {
		return nil, fmt.Errorf("plugin not found: %s", id)
	}
	return p, nil
}

type permissiveCapRegistry struct{}

func (p *permissiveCapRegistry) HasCapability(pluginID types.PluginID, capability string) bool {
	return true
}

type permissivePolicyStore struct{}

func (p *permissivePolicyStore) GetGrant(pluginID types.PluginID, capability string) (*permission.PermissionGrant, error) {
	return &permission.PermissionGrant{Mode: "allow"}, nil
}

// dispatchAuditBridge converts dispatcher.AuditLogger calls to structured
// logs.AuditEntry records.
type dispatchAuditBridge struct {
	inner *logs.AuditLogger
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

	if !allowed {
		log.Printf("[AUDIT] DENY  %s.%s by %s/%s — %s", req.PluginID, req.Capability, req.Actor.Type, req.Actor.ID, detail)
	}
}
