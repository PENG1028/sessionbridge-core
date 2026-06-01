package capability

// KnownCapabilities is the DESIGNED set of all capabilities known to Core.
// It intentionally includes capabilities not yet implemented in the executor.
// The executor registers its IMPLEMENTED subset via Registry.registerDefaults().
var KnownCapabilities = map[string]bool{
	// Node
	"node.list":              true,
	"node.info":              true,
	"node.health":            true,
	"node.disconnect":        true,
	"node.identity.get":      true,
	"node.invite.create":     true,
	"node.invite.list":       true,
	"node.invite.revoke":     true,
	"node.invite.accept":     true,
	"node.peer.list":         true,
	"node.peer.info":         true,
	"node.peer.reconnect":    true,
	"node.peer.disconnect":   true,
	"node.peer.revoke":       true,
	"node.reachability.check": true,

	// Session
	"session.create":  true,
	"session.list":    true,
	"session.get":     true,
	"session.info":    true,
	"session.destroy": true,

	// Session history
	"session.history.getPolicy":     true,
	"session.history.setPolicy":     true,
	"session.history.stats":         true,
	"session.history.list":          true,
	"session.history.clear.plan":    true,
	"session.history.clear.execute": true,

	// Process
	"process.spawn":  true,
	"process.signal": true,
	"process.resize": true,
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
	"fs.remove": true,
	"fs.stat":   true,
	"fs.mkdir":  true,
	"fs.rename": true,

	// Env
	"env.checkBinary": true,
	"env.which":       true,
	"env.home":        true,
	"env.cwd":         true,
	"env.get":         true,
	"env.set":         true,
	"env.list":        true,
	"env.unset":       true,

	// Config
	"config.get":  true,
	"config.set":  true,
	"config.list": true,
	"config.reset": true,

	// Logs
	"logs.tail":  true,
	"logs.query": true,

	// Audit
	"audit.list": true,

	// Notify
	"notify.send":    true,
	"notify.request": true,
	"notify.respond": true,
	"approval.list":  true,

	// System
	"system.info": true,

	// Task
	"task.list": true,
	"task.info": true,

	// Run
	"run.create":       true,
	"run.list":         true,
	"run.info":         true,
	"run.stop":         true,
	"run.updatePolicy": true,
	"run.attach":       true,

	// Update
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
	"process.spawn":  true,
	"stream.write":   true,
	"fs.write":       true,
	"fs.remove":      true,
	"config.set":     true,
	"node.disconnect": true,
	"network.connect": true,
	"network.listen":  true,
	"network.dns":     true,
	"network.proxy":   true,
	"network.fetch":   true,
}
