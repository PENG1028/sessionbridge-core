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
var AllPluginsCaps = map[types.PluginID][]string{
	"shell": {
		"session.create",
		"session.destroy",
		"session.list",
		"session.info",
		"session.get",
		"stream.subscribe",
		"stream.write",
		"stream.list",
		"stream.replay",
		"stream.tail",
		"process.spawn",
		"process.signal",
		"process.resize",
		"process.list",
		"env.get",
		"env.set",
		"env.list",
		"env.unset",
		"env.checkBinary",
		"env.which",
		"env.home",
		"env.cwd",
	},
	"sessionnode-core": {
		"system.info",
		"plugin.list",
		"plugin.get",
		"plugin.info",
		"plugin.status",
		"plugin.check",
		"plugin.enable",
		"plugin.disable",
		"plugin.install.plan",
		"plugin.install.execute",
		"plugin.uninstall",
		"plugin.cache.list",
		"plugin.cache.info",
		"plugin.cache.clear",
		"plugin.cache.clear.plan",
		"plugin.cache.clear.execute",
		"plugin.files.list",
		"plugin.files.register",
		"plugin.permissions.list",
		"plugin.permissions.grant",
		"plugin.permissions.revoke",
		"plugin.config.get",
		"plugin.config.set",
		"plugin.config.schema",
		"plugin.history",
		"node.list",
		"node.info",
		"node.health",
		"session.list",
		"session.info",
		"session.get",
		"session.history.getPolicy",
		"session.history.setPolicy",
		"session.history.stats",
		"session.history.list",
		"session.history.clear.plan",
		"session.history.clear.execute",
		"notify.send",
		"notify.request",
		"notify.respond",
		"node.peer.list",
		"node.peer.info",
		"node.peer.reconnect",
		"node.peer.disconnect",
		"node.peer.revoke",
		"node.reachability.check",
			"node.identity.get",
			"node.invite.create",
			"node.invite.list",
			"node.invite.revoke",
			"node.invite.accept",
	},
	"file-explorer": {
		"fs.read",
		"fs.write",
		"fs.list",
		"fs.mkdir",
		"fs.remove",
		"fs.rename",
		"fs.stat",
	},
	"session": {
		"session.create",
		"session.destroy",
		"session.list",
		"session.info",
		"session.get",
		"stream.subscribe",
		"stream.write",
		"stream.list",
		"stream.replay",
		"stream.tail",
		"session.history.getPolicy",
		"session.history.setPolicy",
		"session.history.stats",
		"session.history.list",
		"session.history.clear.plan",
		"session.history.clear.execute",
	},
	"claude-code": {
		"session.create", "session.list", "session.stop",
		"session.history.getPolicy", "session.history.setPolicy", "session.history.stats", "session.history.list",
		"fs.read", "fs.write", "fs.list", "fs.stat",
		"process.spawn",
		"config.get",
		"notify.send", "notify.request",
		"network.connect", "network.dns",
		"plugin.install.execute", "plugin.cache.clear.execute",
	},
}
