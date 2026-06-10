package executor

import (
	"github.com/PENG1028/sessionbridge-core/pkg/types"
)

// approvalList returns pending approval requests from the notifier.
// This is a thin facade over notify.Manager — no separate approval system.
func approvalList(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.Notifier == nil {
		return map[string]interface{}{
			"approvals": []interface{}{},
		}, nil
	}
	return deps.Notifier.ListApprovals(), nil
}
