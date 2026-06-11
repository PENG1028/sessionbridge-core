package oplog

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/PENG1028/sessionbridge-core/internal/content"
	"github.com/PENG1028/sessionbridge-core/pkg/types"
)

// ErrAlreadyRolledBack is returned when attempting to rollback an already-rolled-back operation.
var ErrAlreadyRolledBack = fmt.Errorf("operation already rolled back")

// ErrOpNotFound is returned when the operation is not found.
var ErrOpNotFound = fmt.Errorf("operation not found")

// ErrNoInverse is returned when no inverse function is registered for the capability.
var ErrNoInverse = fmt.Errorf("no inverse function registered for this capability")

// RollbackEngine provides rollback capabilities for logged operations.
// It uses the OpLog to find operations and the Content Store to restore data.
type RollbackEngine struct {
	OpLog   *Store
	Content *content.Store
}

// NewRollbackEngine creates a RollbackEngine.
func NewRollbackEngine(opLog *Store, contentStore *content.Store) *RollbackEngine {
	return &RollbackEngine{
		OpLog:   opLog,
		Content: contentStore,
	}
}

// RollbackPreview describes what a rollback would do, without executing it.
type RollbackPreview struct {
	OpID         types.OpID `json:"opId"`
	Capability   string     `json:"capability"`
	Class        string     `json:"class"`
	WillRestore  []string   `json:"willRestore,omitempty"`  // A-class: files to restore
	WillDelete   []string   `json:"willDelete,omitempty"`   // B-class: files to delete
	WillRevert   []string   `json:"willRevert,omitempty"`   // C-class: states to revert
	NotSupported bool       `json:"notSupported,omitempty"` // true if no inverse exists
	Reason       string     `json:"reason,omitempty"`       // why rollback is not supported
}

// DryRun analyzes what would happen if the operation were rolled back.
// It does not execute the rollback.
func (e *RollbackEngine) DryRun(opID types.OpID) (*RollbackPreview, error) {
	op := e.OpLog.Get(opID)
	if op == nil {
		return nil, ErrOpNotFound
	}

	preview := &RollbackPreview{
		OpID:       op.OpID,
		Capability: op.Capability,
		Class:      string(op.Class),
	}

	if op.RolledBackAt != 0 {
		preview.Reason = "already rolled back"
		return preview, nil
	}

	_, hasInverse := inverseOps[op.Capability]

	switch op.Class {
	case types.OpClassContent:
		if op.ContentBefore != nil {
			preview.WillRestore = append(preview.WillRestore, op.ContentBefore.Path)
		} else {
			preview.Reason = "no content backup"
		}
	case types.OpClassArtifact:
		for _, art := range op.Artifacts {
			preview.WillDelete = append(preview.WillDelete, art.Path)
		}
		if len(op.Artifacts) == 0 {
			preview.Reason = "no artifact references"
		}
	case types.OpClassState:
		if op.StateBefore != nil {
			preview.WillRevert = append(preview.WillRevert, op.Capability+": "+op.StateBefore.StateID)
		} else {
			preview.Reason = "no state snapshot"
		}
	}

	if !hasInverse {
		preview.NotSupported = true
		if preview.Reason == "" {
			preview.Reason = "no inverse registered"
		}
	}

	return preview, nil
}

// Rollback undoes a single operation by its OpID.
// Returns the operation that was rolled back.
// The rollback event itself is recorded as a new OpLog entry.
func (e *RollbackEngine) Rollback(opID types.OpID) (*types.Operation, error) {
	op := e.OpLog.Get(opID)
	if op == nil {
		return nil, ErrOpNotFound
	}
	if op.RolledBackAt != 0 {
		return nil, ErrAlreadyRolledBack
	}

	// Find and execute inverse function
	fn, ok := inverseOps[op.Capability]
	if !ok {
		return nil, ErrNoInverse
	}
	if err := fn(op, e); err != nil {
		return nil, fmt.Errorf("inverse %s: %w", op.Capability, err)
	}

	// Mark as rolled back (in-memory)
	op.RolledBackAt = time.Now().UnixMilli()

	// Record the rollback event in OpLog
	rollbackEvent := &types.Operation{
		Class:      types.OpClassNoop,
		Capability: "_rollback",
		Params:     mustMarshal(map[string]string{"rolledBackOpId": string(opID)}),
		Actor:      types.Actor{Type: "system", ID: "rollback-engine"},
		Timestamp:  op.RolledBackAt,
	}
	e.OpLog.Append(rollbackEvent)

	return op, nil
}

// RollbackRange undoes all operations within the given time range, in reverse order.
// Returns the list of OpIDs that were rolled back.
func (e *RollbackEngine) RollbackRange(from, to time.Time) ([]types.OpID, error) {
	ops := e.OpLog.Query(types.OpFilter{
		FromTime: from.UnixMilli(),
		ToTime:   to.UnixMilli(),
		Classes:  []types.OpClass{types.OpClassContent, types.OpClassArtifact, types.OpClassState},
	})

	var rolledBack []types.OpID
	// Roll back in reverse order (most recent first)
	for i := len(ops) - 1; i >= 0; i-- {
		if ops[i].RolledBackAt != 0 {
			continue // skip already-rolled-back
		}
		rolled, err := e.Rollback(ops[i].OpID)
		if err != nil {
			return rolledBack, fmt.Errorf("rollback %s: %w", ops[i].OpID, err)
		}
		rolledBack = append(rolledBack, rolled.OpID)
	}
	return rolledBack, nil
}

// mustMarshal is a helper that marshals a value to JSON (panics on error).
func mustMarshal(v interface{}) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
