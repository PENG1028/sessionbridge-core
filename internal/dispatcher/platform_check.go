package dispatcher

import (
	"github.com/user/sessionnode/go-core/internal/capability"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// CapabilitySupportChecker checks whether a capability is supported on a given target node.
// If nil (not wired into the Dispatcher), platform checks are skipped — the executor's
// own platform gating handles the local case.
//
// This interface is optional infrastructure intended for later use when the dispatcher
// needs to pre-validate capability support before forwarding to remote nodes.
type CapabilitySupportChecker interface {
	CheckCapability(capability string, targetNodeID types.NodeID) capability.CapabilitySupport
}
