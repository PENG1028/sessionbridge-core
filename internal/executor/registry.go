package executor

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/PENG1028/sessionbridge-core/internal/capability"
	"github.com/PENG1028/sessionbridge-core/internal/config"
	"github.com/PENG1028/sessionbridge-core/internal/history"
	"github.com/PENG1028/sessionbridge-core/internal/logs"
	"github.com/PENG1028/sessionbridge-core/internal/mesh"
	"github.com/PENG1028/sessionbridge-core/internal/notify"
	"github.com/PENG1028/sessionbridge-core/internal/oplog"
	"github.com/PENG1028/sessionbridge-core/internal/plan"
	"github.com/PENG1028/sessionbridge-core/internal/platform"
	"github.com/PENG1028/sessionbridge-core/internal/process"
	"github.com/PENG1028/sessionbridge-core/internal/run"
	"github.com/PENG1028/sessionbridge-core/internal/session"
	"github.com/PENG1028/sessionbridge-core/internal/task"
	"github.com/PENG1028/sessionbridge-core/internal/update"
	"github.com/PENG1028/sessionbridge-core/internal/wsconn"
	"github.com/PENG1028/sessionbridge-core/pkg/types"
)

// ExecFunc is a handler for a single capability.
type ExecFunc func(req *types.CapabilityRequest, deps *Deps) (interface{}, error)

// NodeInfo describes a known node for the node.list capability.
type NodeInfo struct {
	ID                   types.NodeID `json:"nodeId"`
	Name                 string       `json:"name"`
	Address              string       `json:"address"`
	Tags                 []string     `json:"tags"`
	Status               string       `json:"status"`
	DisplayName          string       `json:"displayName"`
	Role                 string       `json:"role,omitempty"`       // "relay" or "leaf" — set for local node
	InboundPeerReachable bool         `json:"inboundPeerReachable"` // can accept /peer/ws connections
}

// NodeLister provides the set of known peers to the executor.
type NodeLister interface {
	ListNodes() []NodeInfo
}

// TopologyManager provides dynamic peer lifecycle management to the executor.
type TopologyManager interface {
	AddOrUpdatePeer(id types.NodeID, address string, autoReconnect bool) error
	ConnectPeer(nodeID types.NodeID) error
	DisconnectPeer(nodeID types.NodeID) error
	RemovePeer(nodeID types.NodeID) error
	ReconnectPeer(nodeID types.NodeID) error
}

// Deps holds shared dependencies that executors can use.
type Deps struct {
	Sessions   *session.Store
	Processes  *process.Manager
	ConnRoutes *wsconn.Registry
	Notifier   *notify.Manager
	Config     *config.Manager
	Nodes      NodeLister
	History    *history.Store
	// CapResolver resolves capability support against the current platform.
	// When nil, capability support checking is skipped (graceful degradation).
	CapResolver *capability.Resolver
	// TaskStore tracks long-running operations (install, uninstall, check, etc.).
	// When nil, task capabilities degrade gracefully (empty list / not-found error).
	TaskStore *task.Store
	// PlanManager handles approval plan lifecycle (approve, deny) for high-risk operations.
	// When nil, plan-linked approval is skipped (graceful degradation).
	PlanManager *plan.Manager
	// RunStore indexes long-lived execution resources (terminal sessions, processes, etc.).
	// When nil, run capabilities degrade gracefully (empty list / not-found error).
	RunStore *run.Store
	// Mesh bundles cryptographic node identity and the trusted peer store.
	// When nil, mesh/peer capabilities degrade gracefully.
	Mesh *mesh.MeshState
	// LogBuffer is the in-memory ring buffer for logs.tail / logs.query.
	// When nil, log capabilities return empty results.
	LogBuffer *logs.Buffer
	// AuditStore is the in-memory store for audit.list.
	// When nil, audit.list returns empty results.
	AuditStore *logs.AuditStore
	// OpLog is the persistent operation log for restart recovery and audit queries.
	// When set, audit.list queries from OpLog instead of AuditStore.
	OpLog *oplog.Store
	// RollbackEngine provides rollback/dry-run/verify for logged operations.
	RollbackEngine *oplog.RollbackEngine
	// UpdateManager holds update source, policy, and status.
	// When nil, update.* capabilities degrade gracefully.
	UpdateManager *update.Manager
	// GitRunner is used by update.check/update.plan for git operations.
	// When nil, update.check/plan degrade gracefully.
	GitRunner update.GitRunner
	// Topology provides dynamic peer lifecycle management (connect, disconnect, etc.).
	// When nil, peer management capabilities degrade gracefully.
	Topology TopologyManager
}

// RegistryConfig controls which capability groups are registered.
// Build-time composition — different products (standalone/hub/leaf) get different sets.
type RegistryConfig struct {
	Process  bool // process.spawn/signal/resize/list
	Update   bool // update.check/plan/status
	Network  bool // network.* (not yet implemented)
	Sync     bool // sync.diff/apply (not yet implemented)
	Admin    bool // admin.* (not yet implemented)
}

// DefaultRegistryConfig enables all capability groups.
var DefaultRegistryConfig = RegistryConfig{
	Process: true,
	Update:  true,
}

// Registry maps capability names to handler functions.
// Implements the dispatcher.Executor interface.
type Registry struct {
	handlers map[string]ExecFunc
	deps     *Deps
}

// New creates a Registry with all built-in capability handlers
// registered using DefaultRegistryConfig.
func New(deps *Deps) *Registry {
	return NewWithConfig(deps, DefaultRegistryConfig)
}

// NewWithConfig creates a Registry with capability groups controlled by cfg.
func NewWithConfig(deps *Deps, cfg RegistryConfig) *Registry {
	r := &Registry{
		handlers: make(map[string]ExecFunc),
		deps:     deps,
	}
	r.registerCore()
	r.registerConditional(cfg)
	return r
}

// observabilityCapabilities are capabilities that read logs/audit data.
// Requests for these are not themselves logged to avoid feedback noise.
var observabilityCapabilities = map[string]bool{
	"logs.tail":  true,
	"logs.query": true,
	"audit.list": true,
}

// Execute dispatches a capability request to the registered handler.
func (r *Registry) Execute(req *types.CapabilityRequest) (interface{}, error) {
	plat := platform.Current()
	resolver := capability.Resolver{Platform: plat}
	cs := resolver.CheckCapability(req.Capability)
	if !cs.Supported {
		r.recordLog(req, "error", fmt.Sprintf("capability %q unsupported on %s", req.Capability, plat.OS))
		return nil, &types.CoreError{
			Code:    "CAPABILITY_UNSUPPORTED_ON_PLATFORM",
			Message: fmt.Sprintf("capability %q is not supported on %s", req.Capability, plat.OS),
		}
	}

	handler, ok := r.handlers[req.Capability]
	if !ok {
		r.recordLog(req, "error", fmt.Sprintf("unknown capability: %q", req.Capability))
		return nil, fmt.Errorf("unknown capability: %q", req.Capability)
	}

	result, err := handler(req, r.deps)
	if err != nil {
		r.recordLog(req, "error", err.Error())
	} else {
		r.recordLog(req, "info", "ok")
	}
	return result, err
}

func (r *Registry) recordLog(req *types.CapabilityRequest, level, msg string) {
	if r.deps.LogBuffer == nil {
		return
	}
	if observabilityCapabilities[req.Capability] {
		return
	}
	r.deps.LogBuffer.Add(logs.Entry{
		Timestamp: time.Now().UnixMilli(),
		Level:     level,
		Source:    "core",
		PluginID:  string(req.PluginID),
		SessionID: extractSessionIDFromPayload(req.Payload),
		Message:   fmt.Sprintf("%s %s", req.Capability, msg),
	})
}

func extractSessionIDFromPayload(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(payload, &m); err != nil {
		return ""
	}
	if sid, ok := m["sessionId"].(string); ok {
		return sid
	}
	return ""
}

// Register adds a handler for the given capability.
func (r *Registry) Register(capability string, fn ExecFunc) {
	r.handlers[capability] = fn
}

// registerCore registers capabilities that are always available.
func (r *Registry) registerCore() {
	// Session
	r.Register("session.create", sessionCreate)
	r.Register("session.destroy", sessionDestroy)
	r.Register("session.list", sessionList)
	r.Register("session.info", sessionInfo)
	r.Register("session.get", sessionGet)

	// Stream
	r.Register("stream.subscribe", streamSubscribe)
	r.Register("stream.write", streamWrite)
	r.Register("stream.list", streamList)
	r.Register("stream.replay", streamReplay)
	r.Register("stream.tail", streamTail)

	// Filesystem
	r.Register("fs.read", fsRead)
	r.Register("fs.write", fsWrite)
	r.Register("fs.list", fsList)
	r.Register("fs.mkdir", fsMkdir)
	r.Register("fs.remove", fsRemove)
	r.Register("fs.rename", fsRename)
	r.Register("fs.stat", fsStat)

	// Env
	r.Register("env.get", envGet)
	r.Register("env.set", envSet)
	r.Register("env.list", envList)
	r.Register("env.unset", envUnset)
	r.Register("env.checkBinary", envCheckBinary)
	r.Register("env.which", envWhich)
	r.Register("env.home", envHome)
	r.Register("env.cwd", envCwd)

	// Config
	r.Register("config.list", configList)
	r.Register("config.get", configGet)
	r.Register("config.set", configSet)
	r.Register("config.reset", configReset)

	// System
	r.Register("system.info", systemInfo)

	// Node
	r.Register("node.list", nodeList)
	r.Register("node.info", nodeInfo)
	r.Register("node.health", nodeHealth)

	// Notify & Approval
	r.Register("notify.send", notifySend)
	r.Register("notify.request", notifyRequest)
	r.Register("notify.respond", notifyRespond)
	r.Register("approval.list", approvalList)

	// History
	r.Register("session.history.getPolicy", historyGetPolicy)
	r.Register("session.history.setPolicy", historySetPolicy)
	r.Register("session.history.stats", historyStats)
	r.Register("session.history.list", historyList)
	r.Register("session.history.clear.plan", historyClearPlan)
	r.Register("session.history.clear.execute", historyClearExecute)

	// Observability
	r.Register("logs.tail", logsTail)
	r.Register("logs.query", logsQuery)
	r.Register("audit.list", auditList)

	// Task
	r.Register("task.list", taskList)
	r.Register("task.info", taskInfo)

	// Run
	r.Register("run.create", runCreate)
	r.Register("run.list", runList)
	r.Register("run.info", runInfo)
	r.Register("run.stop", runStop)
	r.Register("run.updatePolicy", runUpdatePolicy)
	r.Register("run.attach", runAttach)

	// Peer & Mesh
	r.Register("node.peer.list", nodePeerList)
	r.Register("node.peer.info", nodePeerInfo)
	r.Register("node.peer.reconnect", nodePeerReconnect)
	r.Register("node.peer.disconnect", nodePeerDisconnect)
	r.Register("node.peer.revoke", nodePeerRevoke)
	r.Register("node.reachability.check", nodeReachabilityCheck)
	r.Register("node.identity.get", nodeIdentityGet)
	r.Register("node.invite.create", nodeInviteCreate)
	r.Register("node.invite.list", nodeInviteList)
	r.Register("node.invite.revoke", nodeInviteRevoke)
	r.Register("node.invite.accept", nodeInviteAccept)

	// Operation Log
	r.Register("operations.list", operationsList)
	r.Register("operations.get", operationsGet)
	r.Register("operations.dryRun", operationsDryRun)
	r.Register("operations.rollback", operationsRollback)
	r.Register("operations.rollbackRange", operationsRollbackRange)
	r.Register("operations.verify", operationsVerify)

	// Desktop — AI 电脑操作基础能力
}

// registerConditional registers capabilities based on RegistryConfig flags.
func (r *Registry) registerConditional(cfg RegistryConfig) {
	if cfg.Process {
		r.Register("process.spawn", processSpawn)
		r.Register("process.signal", processSignal)
		r.Register("process.resize", processResize)
		r.Register("process.list", processList)
	}
	if cfg.Update {
		r.Register("update.status", updateStatus)
		r.Register("update.source.get", updateSourceGet)
		r.Register("update.source.set", updateSourceSet)
		r.Register("update.policy.get", updatePolicyGet)
		r.Register("update.policy.set", updatePolicySet)
		r.Register("update.check", updateCheck)
		r.Register("update.plan", updatePlan)
		r.Register("update.ignore", updateIgnore)
	}
}
