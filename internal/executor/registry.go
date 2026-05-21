package executor

import (
	"fmt"

	"github.com/user/sessionnode/go-core/internal/capability"
	"github.com/user/sessionnode/go-core/internal/config"
	"github.com/user/sessionnode/go-core/internal/history"
	"github.com/user/sessionnode/go-core/internal/mesh"
	"github.com/user/sessionnode/go-core/internal/notify"
	"github.com/user/sessionnode/go-core/internal/plan"
	"github.com/user/sessionnode/go-core/internal/platform"
	"github.com/user/sessionnode/go-core/internal/pluginmanifest"
	"github.com/user/sessionnode/go-core/internal/process"
	"github.com/user/sessionnode/go-core/internal/run"
	"github.com/user/sessionnode/go-core/internal/session"
	"github.com/user/sessionnode/go-core/internal/task"
	"github.com/user/sessionnode/go-core/internal/wsconn"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// ExecFunc is a handler for a single capability.
type ExecFunc func(req *types.CapabilityRequest, deps *Deps) (interface{}, error)

// NodeInfo describes a known node for the node.list capability.
type NodeInfo struct {
	ID          types.NodeID `json:"nodeId"`
	Name        string       `json:"name"`
	Address     string       `json:"address"`
	Tags        []string     `json:"tags"`
	Status      string       `json:"status"`
	DisplayName string       `json:"displayName"`
}

// NodeLister provides the set of known peers to the executor.
type NodeLister interface {
	ListNodes() []NodeInfo
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
	Manifests  ManifestLoader
	// CapResolver resolves capability support against the current platform.
	// When nil, capability support checking is skipped (graceful degradation).
	CapResolver *capability.Resolver
	// TaskStore tracks long-running operations (install, uninstall, check, etc.).
	// When nil, task capabilities degrade gracefully (empty list / not-found error).
	TaskStore *task.Store
	// Store holds install plans and registered plugin file paths.
	Store *PlanStore
	// PlanManager handles approval plan lifecycle (approve, deny) for high-risk operations.
	// When nil, plan-linked approval is skipped (graceful degradation).
	PlanManager *plan.Manager
	// RunStore indexes long-lived execution resources (terminal sessions, processes, etc.).
	// When nil, run capabilities degrade gracefully (empty list / not-found error).
	RunStore *run.Store
	// Mesh bundles cryptographic node identity and the trusted peer store.
	// When nil, mesh/peer capabilities degrade gracefully.
	Mesh *mesh.MeshState
}

// ManifestLoader provides plugin manifest data to capability handlers.
// Phase 1: may be nil — handlers degrade gracefully.
type ManifestLoader interface {
	LoadManifest(pluginID string) (*pluginmanifest.Manifest, error)
	ListPlugins() []pluginmanifest.PluginSummary
	PluginEnabled(pluginID string) bool
}

// Registry maps capability names to handler functions.
// Implements the dispatcher.Executor interface.
type Registry struct {
	handlers map[string]ExecFunc
	deps     *Deps
}

// New creates a Registry with all built-in capability handlers registered.
func New(deps *Deps) *Registry {
	r := &Registry{
		handlers: make(map[string]ExecFunc),
		deps:     deps,
	}
	r.registerDefaults()
	return r
}

// Execute dispatches a capability request to the registered handler.
func (r *Registry) Execute(req *types.CapabilityRequest) (interface{}, error) {
	plat := platform.Current()
	resolver := capability.Resolver{Platform: plat}
	cs := resolver.CheckCapability(req.Capability)
	if !cs.Supported {
		return nil, &types.CoreError{
			Code:    "CAPABILITY_UNSUPPORTED_ON_PLATFORM",
			Message: fmt.Sprintf("capability %q is not supported on %s", req.Capability, plat.OS),
		}
	}

	handler, ok := r.handlers[req.Capability]
	if !ok {
		return nil, fmt.Errorf("unknown capability: %q", req.Capability)
	}
	return handler(req, r.deps)
}

// Register adds a handler for the given capability.
func (r *Registry) Register(capability string, fn ExecFunc) {
	r.handlers[capability] = fn
}

func (r *Registry) registerDefaults() {
	r.Register("session.create", sessionCreate)
	r.Register("session.destroy", sessionDestroy)
	r.Register("session.list", sessionList)
	r.Register("session.info", sessionInfo)

	r.Register("stream.subscribe", streamSubscribe)
	r.Register("stream.write", streamWrite)
	r.Register("stream.list", streamList)

	r.Register("fs.read", fsRead)
	r.Register("fs.write", fsWrite)
	r.Register("fs.list", fsList)
	r.Register("fs.mkdir", fsMkdir)
	r.Register("fs.remove", fsRemove)
	r.Register("fs.rename", fsRename)

	r.Register("process.spawn", processSpawn)
	r.Register("process.signal", processSignal)
	r.Register("process.resize", processResize)
	r.Register("process.list", processList)

	r.Register("env.get", envGet)
	r.Register("env.set", envSet)
	r.Register("env.list", envList)
	r.Register("env.unset", envUnset)

	r.Register("config.list", configList)
	r.Register("config.get", configGet)
	r.Register("config.set", configSet)
	r.Register("config.reset", configReset)

	r.Register("system.info", systemInfo)

	r.Register("plugin.list", pluginList)
	r.Register("plugin.info", pluginInfo)
	r.Register("plugin.get", pluginGet)
	r.Register("plugin.status", pluginStatus)
	r.Register("plugin.check", pluginCheck)
	r.Register("plugin.enable", pluginEnable)
	r.Register("plugin.disable", pluginDisable)
	r.Register("plugin.install", pluginInstallPlan)
	r.Register("plugin.install.plan", pluginInstallPlan)
	r.Register("plugin.permissions.list", pluginPermissionsList)
	r.Register("plugin.config.get", pluginConfigGet)
	r.Register("plugin.config.schema", pluginConfigSchema)
	r.Register("plugin.history", pluginHistory)
	r.Register("plugin.cache.list", pluginCacheList)
	r.Register("plugin.cache.info", pluginCacheInfo)
	r.Register("plugin.cache.clear", pluginCacheClear)
	r.Register("plugin.cache.clear.plan", pluginCacheClearPlan)
	r.Register("plugin.cache.clear.execute", pluginCacheClearExecute)
	r.Register("plugin.files.list", pluginFilesList)
	r.Register("plugin.files.register", pluginFilesRegister)
	r.Register("plugin.install.execute", pluginInstallExecute)
	r.Register("plugin.uninstall", pluginUninstall)
	r.Register("plugin.permissions.grant", pluginPermissionsGrant)
	r.Register("plugin.permissions.revoke", pluginPermissionsRevoke)
	r.Register("plugin.config.set", pluginConfigSet)

	r.Register("node.list", nodeList)
	r.Register("node.info", nodeInfo)
	r.Register("node.health", nodeHealth)

	r.Register("session.get", sessionGet)

	r.Register("fs.stat", fsStat)

	r.Register("env.checkBinary", envCheckBinary)
	r.Register("env.which", envWhich)
	r.Register("env.home", envHome)
	r.Register("env.cwd", envCwd)
	r.Register("notify.send", notifySend)
	r.Register("notify.request", notifyRequest)
	r.Register("notify.respond", notifyRespond)

	r.Register("approval.list", approvalList)

	// History & Replay
	r.Register("session.history.getPolicy", historyGetPolicy)
	r.Register("session.history.setPolicy", historySetPolicy)
	r.Register("session.history.stats", historyStats)
	r.Register("session.history.list", historyList)
	r.Register("session.history.clear.plan", historyClearPlan)
	r.Register("session.history.clear.execute", historyClearExecute)
		r.Register("stream.replay", streamReplay)
		r.Register("stream.tail", streamTail)

		r.Register("logs.tail", logsTail)
		r.Register("logs.query", logsQuery)
		r.Register("audit.list", auditList)

		r.Register("task.list", taskList)
		r.Register("task.info", taskInfo)

	r.Register("run.create", runCreate)
	r.Register("run.list", runList)
	r.Register("run.info", runInfo)
	r.Register("run.stop", runStop)
	r.Register("run.updatePolicy", runUpdatePolicy)

	r.Register("node.peer.list", nodePeerList)
	r.Register("node.peer.info", nodePeerInfo)
	r.Register("node.peer.reconnect", nodePeerReconnect)
	r.Register("node.peer.disconnect", nodePeerDisconnect)
	r.Register("node.peer.revoke", nodePeerRevoke)
	r.Register("node.reachability.check", nodeReachabilityCheck)

	// Mesh: identity & invite
	r.Register("node.identity.get", nodeIdentityGet)
	r.Register("node.invite.create", nodeInviteCreate)
	r.Register("node.invite.list", nodeInviteList)
	r.Register("node.invite.revoke", nodeInviteRevoke)
	r.Register("node.invite.accept", nodeInviteAccept)
}
