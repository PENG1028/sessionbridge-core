package dispatcher

import (
	"github.com/user/sessionnode/go-core/pkg/protocol"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// --- Interfaces (consumer-defined — Go idiom) ---

// Authenticator validates an actor's credentials.
type Authenticator interface {
	Authenticate(actor types.Actor) (*types.Actor, error)
}

// PluginRegistry resolves plugins by ID.
type PluginRegistry interface {
	Get(id types.PluginID) (*PluginEntry, error)
}

// PermissionChecker checks whether a capability request is authorized.
type PermissionChecker interface {
	Check(req *types.CapabilityRequest) error
}

// Executor runs a capability and returns the result.
type Executor interface {
	Execute(req *types.CapabilityRequest) (interface{}, error)
}

// Planner handles "Plan Before Apply" for high-risk capabilities.
// When Check returns a non-nil plan ID, the capability must go through
// plan creation → approval → execution rather than executing directly.
type Planner interface {
	// RequiresPlan returns true if the capability needs a plan before execution.
	RequiresPlan(capability string) bool
	// CreatePlan creates a pending plan for the request. Returns the plan ID.
	CreatePlan(req *types.CapabilityRequest) (string, error)
}

// AuditLogger records capability calls for the audit trail.
type AuditLogger interface {
	Log(req *types.CapabilityRequest, allowed bool, detail string)
}

// Topology resolves target nodes for remote routing.
type Topology interface {
	Get(nodeID types.NodeID) (*NodeTarget, error)
}

// --- Internal types ---

// PluginEntry is the internal representation of a plugin held by the Dispatcher.
type PluginEntry struct {
	ID      types.PluginID
	Enabled bool
}

// NodeTarget describes a reachable remote node and how to forward requests to it.
type NodeTarget struct {
	ID      types.NodeID
	Forward func(req *types.CapabilityRequest) (*types.CapabilityResponse, error)
}

// --- Dispatcher ---

// Dispatcher implements the 8-step capability dispatch chain:
// authenticate → resolve plugin → check enabled → check permission →
// plan check → route to target → execute → audit → return.
type Dispatcher struct {
	auth        Authenticator
	plugins     PluginRegistry
	permissions PermissionChecker
	planner     Planner
	executor    Executor
	audit       AuditLogger
	topology    Topology
	localNodeID types.NodeID
}

// New creates a Dispatcher with the given component implementations.
// planner may be nil if Plan Before Apply is not configured.
func New(
	auth Authenticator,
	plugins PluginRegistry,
	permissions PermissionChecker,
	planner Planner,
	executor Executor,
	audit AuditLogger,
	topology Topology,
	localNodeID types.NodeID,
) *Dispatcher {
	return &Dispatcher{
		auth:        auth,
		plugins:     plugins,
		permissions: permissions,
		planner:     planner,
		executor:    executor,
		audit:       audit,
		topology:    topology,
		localNodeID: localNodeID,
	}
}

// Dispatch runs the full capability execution chain.
func (d *Dispatcher) Dispatch(req *types.CapabilityRequest) *types.CapabilityResponse {
	// Step 1: Authenticate
	actor, err := d.auth.Authenticate(req.Actor)
	if err != nil {
		d.audit.Log(req, false, err.Error())
		return errorResponse(req, protocol.ErrCodeUnauthenticated, err.Error())
	}
	req.Actor = *actor

	// Step 2: Resolve plugin
	plugin, err := d.plugins.Get(req.PluginID)
	if err != nil {
		d.audit.Log(req, false, err.Error())
		return errorResponse(req, protocol.ErrCodePluginNotFound, err.Error())
	}

	// Step 3: Check enabled
	if !plugin.Enabled {
		d.audit.Log(req, false, "plugin disabled")
		return errorResponse(req, protocol.ErrCodePluginDisabled, "plugin is disabled")
	}

	// Step 4: Check permission
	if err := d.permissions.Check(req); err != nil {
		d.audit.Log(req, false, err.Error())
		// Preserve the specific error code (e.g. NODE_NOT_ALLOWED, PATH_NOT_ALLOWED)
		// instead of mapping everything to PERMISSION_DENIED.
		code := protocol.ErrCodePermissionDenied
		if ce, ok := err.(interface{ ErrorCode() string }); ok {
			code = ce.ErrorCode()
		}
		return errorResponse(req, code, err.Error())
	}

	// Step 4.5: Plan Before Apply for high-risk capabilities.
	if d.planner != nil && d.planner.RequiresPlan(req.Capability) && req.PlanID == "" {
		planID, err := d.planner.CreatePlan(req)
		if err != nil {
			d.audit.Log(req, false, "plan creation failed: "+err.Error())
			return errorResponse(req, protocol.ErrCodePlanFailed, err.Error())
		}
		d.audit.Log(req, true, "plan created: "+planID)
		return &types.CapabilityResponse{
			RequestID: req.RequestID,
			OK:        false,
			Error:     &types.CoreError{Code: protocol.ErrCodePlanRequired, Message: "plan required — approve via plan.approve"},
			PlanID:    planID,
			PlanState: "pending",
		}
	}

	// Step 5: Route to target node
	if req.TargetNodeID != "" && req.TargetNodeID != d.localNodeID {
		target, err := d.topology.Get(req.TargetNodeID)
		if err != nil {
			d.audit.Log(req, false, err.Error())
			return errorResponse(req, protocol.ErrCodeNodeUnreachable, err.Error())
		}
		resp, err := target.Forward(req)
		if err != nil {
			d.audit.Log(req, false, err.Error())
			return errorResponse(req, protocol.ErrCodeForwardError, err.Error())
		}
		d.audit.Log(req, true, "routed to "+string(req.TargetNodeID))
		return resp
	}

	// Step 6: Execute locally
	result, err := d.executor.Execute(req)
	if err != nil {
		d.audit.Log(req, false, err.Error())
		return errorResponse(req, protocol.ErrCodeExecutionError, err.Error())
	}

	// Step 7: Audit success
	d.audit.Log(req, true, "")

	// Step 8: Return
	return &types.CapabilityResponse{
		RequestID: req.RequestID,
		OK:        true,
		Payload:   result,
	}
}

func errorResponse(req *types.CapabilityRequest, code, message string) *types.CapabilityResponse {
	return &types.CapabilityResponse{
		RequestID: req.RequestID,
		OK:        false,
		Error:     &types.CoreError{Code: code, Message: message},
	}
}
