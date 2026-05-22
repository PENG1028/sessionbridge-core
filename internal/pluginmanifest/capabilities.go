package pluginmanifest

// KnownCapabilities is the DESIGNED set of all capabilities known to Core.
// It intentionally includes capabilities not yet implemented in the executor.
// The executor registers its IMPLEMENTED subset via Registry.registerDefaults().
// Test TestRegisteredCapabilitiesInKnownList in package executor ensures no
// implemented capability is missing from this list.
var KnownCapabilities = map[string]bool{
	// Node
	"node.list":       true,
	"node.info":       true,
	"node.health":     true,
	"node.disconnect": true,

	// Node — mesh security
	"node.identity.get":       true,
	"node.invite.create":      true,
	"node.invite.list":        true,
	"node.invite.revoke":      true,
	"node.invite.accept":      true,
	"node.peer.list":          true,
	"node.peer.info":          true,
	"node.peer.reconnect":     true,
	"node.peer.disconnect":    true,
	"node.peer.revoke":        true,
	"node.reachability.check": true,

	// Session
	"session.create":  true,
	"session.list":    true,
	"session.get":     true,
	"session.info":    true,
	"session.stop":    true,
	"session.destroy": true,
	"session.events":  true,
	"session.replay":  true,

	// Process
	"process.spawn":  true,
	"process.kill":   true,
	"process.signal": true,
	"process.resize": true,
	"process.status": true,
	"process.list":   true,

	// Stream
	"stream.subscribe": true,
	"stream.write":     true,
	"stream.replay":    true,
	"stream.tail":      true,
	"stream.list":      true,

	// FS
	"fs.list":   true,
	"fs.read":   true,
	"fs.write":  true,
	"fs.delete": true,
	"fs.remove": true,
	"fs.stat":   true,
	"fs.mkdir":  true,
	"fs.rename": true,

	// Env
	"env.info":        true,
	"env.checkBinary": true,
	"env.which":       true,
	"env.home":        true,
	"env.cwd":         true,
	"env.vars":        true,
	"env.get":         true,
	"env.set":         true,
	"env.list":        true,
	"env.unset":       true,

	// Config
	"config.get":   true,
	"config.set":   true,
	"config.list":  true,
	"config.reset": true,
	"config.watch": true,

	// Logs
	"logs.tail":   true,
	"logs.query":  true,
	"logs.export": true,

	// Audit
	"audit.list": true,
	"audit.get":  true,

	// Notify / Approval
	"notify.send":    true,
	"notify.request": true,
	"notify.respond": true,
	"approval.list":  true,

	// Plugin management
	"plugin.list":                true,
	"plugin.get":                 true,
	"plugin.info":                true,
	"plugin.status":              true,
	"plugin.enable":              true,
	"plugin.disable":             true,
	"plugin.check":               true,
	"plugin.install":             true,
	"plugin.install.plan":        true,
	"plugin.install.execute":     true,
	"plugin.uninstall":           true,
	"plugin.files.list":          true,
	"plugin.files.register":      true,
	"plugin.cache.list":          true,
	"plugin.cache.info":          true,
	"plugin.cache.clear":         true,
	"plugin.cache.clear.plan":    true,
	"plugin.cache.clear.execute": true,
	"plugin.permissions.list":    true,
	"plugin.permissions.grant":   true,
	"plugin.permissions.revoke":  true,
	"plugin.config.get":          true,
	"plugin.config.set":          true,
	"plugin.config.schema":       true,
	"plugin.history":             true,

	// System
	"system.info": true,

	// Task
	"task.list": true,
	"task.info": true,

	// Session history
	"session.history.getPolicy":     true,
	"session.history.setPolicy":     true,
	"session.history.stats":         true,
	"session.history.list":          true,
	"session.history.clear.plan":    true,
	"session.history.clear.execute": true,

	// Run
	"run.create":       true,
	"run.list":         true,
	"run.info":         true,
	"run.stop":         true,
	"run.updatePolicy": true,
	"run.attach":       true,

	// Update — self-update status and planning baseline
	"update.status":     true,
	"update.source.get": true,
	"update.source.set": true,
	"update.policy.get": true,
	"update.policy.set": true,
	"update.check":      true,
	"update.plan":       true,
	"update.ignore":     true,

	// Network
	"network.connect": true,
	"network.listen":  true,
	"network.dns":     true,
	"network.proxy":   true,
	"network.fetch":   true,
}

// DangerousCapabilities are capabilities that must NOT have default: allow
// unless the plugin is trusted AND allowDangerousDefaults is explicitly set.
var DangerousCapabilities = map[string]bool{
	"process.spawn":              true,
	"stream.write":               true,
	"fs.write":                   true,
	"fs.delete":                  true,
	"fs.remove":                  true,
	"plugin.install.execute":     true,
	"plugin.cache.clear.execute": true,
	"config.set":                 true,
	"permission.grant":           true,
	"plugin.permissions.grant":   true,
	"node.disconnect":            true,
	"network.connect":            true,
	"network.listen":             true,
	"network.dns":                true,
	"network.proxy":              true,
	"network.fetch":              true,
}

// PermissionDefault values
const (
	DefaultAsk   = "ask"
	DefaultDeny  = "deny"
	DefaultAllow = "allow"
)

// ValidPermissionDefaults lists accepted default values.
var ValidPermissionDefaults = map[string]bool{
	DefaultAsk:   true,
	DefaultDeny:  true,
	DefaultAllow: true,
}

// ReservedPluginIDs are IDs that no plugin may use.
var ReservedPluginIDs = map[string]bool{
	"system-ui":        true,
	"sessionnode-core": true,
}

// kebabCaseRegex is a simple kebab-case validator.
func isKebabCase(s string) bool {
	if len(s) < 1 {
		return false
	}
	for i, r := range s {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '-' && i > 0 && i < len(s)-1 {
			continue
		}
		return false
	}
	return true
}

// isNamespacePrefix checks if id starts with the pluginID + ".".
func isNamespacePrefix(pluginID, id string) bool {
	prefix := pluginID + "."
	return len(id) > len(prefix) && id[:len(prefix)] == prefix
}
