package executor

import (
	"fmt"

	"github.com/user/sessionnode/go-core/internal/config"
	"github.com/user/sessionnode/go-core/internal/notify"
	"github.com/user/sessionnode/go-core/internal/process"
	"github.com/user/sessionnode/go-core/internal/session"
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

	r.Register("system.info", systemInfo)

	r.Register("plugin.list", pluginList)
	r.Register("plugin.info", pluginInfo)

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
}
