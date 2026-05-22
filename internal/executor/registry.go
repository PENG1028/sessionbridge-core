package executor

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/user/sessionnode/go-core/internal/capability"
	"github.com/user/sessionnode/go-core/internal/config"
	"github.com/user/sessionnode/go-core/internal/history"
	"github.com/user/sessionnode/go-core/internal/logs"
	"github.com/user/sessionnode/go-core/internal/mesh"
	"github.com/user/sessionnode/go-core/internal/notify"
	"github.com/user/sessionnode/go-core/internal/plan"
	"github.com/user/sessionnode/go-core/internal/platform"
	"github.com/user/sessionnode/go-core/internal/pluginmanifest"
	"github.com/user/sessionnode/go-core/internal/process"
	"github.com/user/sessionnode/go-core/internal/run"
	"github.com/user/sessionnode/go-core/internal/session"
	"github.com/user/sessionnode/go-core/internal/task"
	"github.com/user/sessionnode/go-core/internal/update"
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
	// LogBuffer is the in-memory ring buffer for logs.tail / logs.query.
	// When nil, log capabilities return empty results.
	LogBuffer *logs.Buffer
	// AuditStore is the in-memory store for audit.list.
	// When nil, audit.list returns empty results.
	AuditStore *logs.AuditStore
	// UpdateManager holds update source, policy, and status.
	// When nil, update.* capabilities degrade gracefully.
	UpdateManager *update.Manager
	// GitRunner is used by update.check/update.plan for git operations.
	// When nil, update.check/plan degrade gracefully.
	GitRunner update.GitRunner
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
		r.recordAudit(req, "error", fmt.Sprintf("unsupported on %s", plat.OS))
		return nil, &types.CoreError{
			Code:    "CAPABILITY_UNSUPPORTED_ON_PLATFORM",
			Message: fmt.Sprintf("capability %q is not supported on %s", req.Capability, plat.OS),
		}
	}

	handler, ok := r.handlers[req.Capability]
	if !ok {
		r.recordLog(req, "error", fmt.Sprintf("unknown capability: %q", req.Capability))
		r.recordAudit(req, "error", "unknown capability")
		return nil, fmt.Errorf("unknown capability: %q", req.Capability)
	}

	result, err := handler(req, r.deps)
	if err != nil {
		r.recordLog(req, "error", err.Error())
		r.recordAudit(req, "error", err.Error())
	} else {
		r.recordLog(req, "info", "ok")
		r.recordAudit(req, "ok", "")
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

func (r *Registry) recordAudit(req *types.CapabilityRequest, outcome, detail string) {
	if r.deps.AuditStore == nil {
		return
	}
	if observabilityCapabilities[req.Capability] {
		return
	}
	actor := req.Actor.Type + ":" + req.Actor.ID
	target := string(req.PluginID) + "/" + req.Capability
	meta := map[string]interface{}{
		"requestId": string(req.RequestID),
	}
	if req.TargetNodeID != "" {
		meta["targetNodeId"] = string(req.TargetNodeID)
	}
	if detail != "" {
		meta["detail"] = detail
	}
	r.deps.AuditStore.Record(logs.AuditRecord{
		Timestamp: time.Now().UnixMilli(),
		EventType: "capability.call",
		Actor:     actor,
		Target:    target,
		Outcome:   outcome,
		Metadata:  meta,
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
	r.Register("run.attach", runAttach)

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

	// Update — self-update status and planning baseline
	r.Register("update.status", updateStatus)
	r.Register("update.source.get", updateSourceGet)
	r.Register("update.source.set", updateSourceSet)
	r.Register("update.policy.get", updatePolicyGet)
	r.Register("update.policy.set", updatePolicySet)
	r.Register("update.check", updateCheck)
	r.Register("update.plan", updatePlan)
	r.Register("update.ignore", updateIgnore)
}
