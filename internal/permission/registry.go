package permission

import (
	"github.com/user/sessionnode/go-core/pkg/types"
)

// MapRegistry is a declarative capability registry that maps plugin IDs to their declared capabilities.
// It implements PluginCapRegistry.
type MapRegistry struct {
	// capabilities maps pluginID -> set of capability strings
	capabilities map[types.PluginID]map[string]bool
}

// NewMapRegistry creates a MapRegistry from a capability map.
// Each entry maps a plugin ID to its list of declared capabilities.
func NewMapRegistry(caps map[types.PluginID][]string) *MapRegistry {
	m := make(map[types.PluginID]map[string]bool, len(caps))
	for pid, list := range caps {
		set := make(map[string]bool, len(list))
		for _, c := range list {
			set[c] = true
		}
		m[pid] = set
	}
	return &MapRegistry{capabilities: m}
}

// HasCapability returns true if the plugin declares the given capability.
func (r *MapRegistry) HasCapability(pluginID types.PluginID, capability string) bool {
	caps, ok := r.capabilities[pluginID]
	if !ok {
		return false
	}
	return caps[capability]
}

// AllPluginsCaps declares all known capabilities for every known plugin.
// This is the v0 capability inventory — grows as more plugins are defined.
//
// sessionnode-core is the core runtime identity and owns all capabilities.
// Other plugin IDs (shell, session, file-explorer, claude-code) are removed
// because no external plugins exist — they were misleading placeholders.
// When real third-party plugins are introduced, they will declare their own
// capability subsets via plugin.yaml manifests.
var AllPluginsCaps = map[types.PluginID][]string{
	"sessionnode-core": {
		// ── System ──
		"system.info",
		// ── Logs / Audit ──
		"logs.tail",
		"logs.query",
		"audit.list",
		// ── Approvals ──
		"approval.list",
		// ── Notifications ──
		"notify.send",
		"notify.request",
		"notify.respond",
		// ── Configuration ──
		"config.get",
		"config.list",
		"config.set",
		"config.reset",
		// ── Node / Mesh ──
		"node.list",
		"node.info",
		"node.health",
		"node.identity.get",
		"node.peer.list",
		"node.peer.info",
		"node.peer.reconnect",
		"node.peer.disconnect",
		"node.peer.revoke",
		"node.reachability.check",
		"node.invite.create",
		"node.invite.list",
		"node.invite.revoke",
		"node.invite.accept",
		// ── Session lifecycle ──
		"session.create",
		"session.destroy",
		"session.list",
		"session.info",
		"session.get",
		"session.history.getPolicy",
		"session.history.setPolicy",
		"session.history.stats",
		"session.history.list",
		"session.history.clear.plan",
		"session.history.clear.execute",
		// ── Stream (stdin/stdout/stderr) ──
		"stream.list",
		"stream.replay",
		"stream.subscribe",
		"stream.tail",
		"stream.write",
		// ── Process ──
		"process.list",
		"process.resize",
		"process.signal",
		"process.spawn",
		// ── Run ──
		"run.list",
		"run.create",
		"run.info",
		"run.stop",
		"run.attach",
		"run.updatePolicy",
		// ── Filesystem ──
		"fs.list",
		"fs.mkdir",
		"fs.read",
		"fs.remove",
		"fs.rename",
		"fs.stat",
		"fs.write",
		// ── Environment ──
		"env.checkBinary",
		"env.cwd",
		"env.get",
		"env.home",
		"env.list",
		"env.set",
		"env.unset",
		"env.which",
		// ── Update ──
		"update.status",
		"update.source.get",
		"update.source.set",
		"update.policy.get",
		"update.policy.set",
		"update.check",
		"update.plan",
		"update.ignore",
	},
}
