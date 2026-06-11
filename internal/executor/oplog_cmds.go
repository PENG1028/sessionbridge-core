package executor

import (
	"fmt"
	"time"

	"github.com/PENG1028/sessionbridge-core/pkg/types"
)

// ─── operations.* API ───────────────────────────────────────────────
//
// These handlers expose the OpLog and RollbackEngine to external callers
// (App UI, CLI, plugins) for querying, rolling back, and verifying operations.

// --- operations.list ---

type operationsListPayload struct {
	Capability string `json:"capability,omitempty"`
	ActorType  string `json:"actorType,omitempty"`
	FromTime   int64  `json:"fromTime,omitempty"` // unix millis
	ToTime     int64  `json:"toTime,omitempty"`   // unix millis
	Class      string `json:"class,omitempty"`    // "A", "B", "C"
	Limit      int    `json:"limit,omitempty"`
}

func operationsList(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.OpLog == nil {
		return map[string]interface{}{"operations": []interface{}{}}, nil
	}

	var p operationsListPayload
	_ = decodePayload(req.Payload, &p)

	var classes []types.OpClass
	switch p.Class {
	case "A":
		classes = []types.OpClass{types.OpClassContent}
	case "B":
		classes = []types.OpClass{types.OpClassArtifact}
	case "C":
		classes = []types.OpClass{types.OpClassState}
	}

	filter := types.OpFilter{
		Classes:    classes,
		Capability: p.Capability,
		ActorType:  p.ActorType,
		FromTime:   p.FromTime,
		ToTime:     p.ToTime,
		Limit:      clampLimit(p.Limit, 50, 500),
	}

	ops := deps.OpLog.Query(filter)
	if ops == nil {
		ops = []*types.Operation{}
	}
	return map[string]interface{}{"operations": ops}, nil
}

// --- operations.get ---

type operationsGetPayload struct {
	OpID string `json:"opId"`
}

func operationsGet(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.OpLog == nil {
		return nil, fmt.Errorf("oplog not available")
	}
	var p operationsGetPayload
	if err := decodePayload(req.Payload, &p); err != nil || p.OpID == "" {
		return nil, fmt.Errorf("opId is required")
	}
	op := deps.OpLog.Get(types.OpID(p.OpID))
	if op == nil {
		return nil, fmt.Errorf("operation not found: %s", p.OpID)
	}
	return map[string]interface{}{"operation": op}, nil
}

// --- operations.dryRun ---

type operationsDryRunPayload struct {
	OpID string `json:"opId"`
}

func operationsDryRun(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.RollbackEngine == nil {
		return nil, fmt.Errorf("rollback engine not available")
	}
	var p operationsDryRunPayload
	if err := decodePayload(req.Payload, &p); err != nil || p.OpID == "" {
		return nil, fmt.Errorf("opId is required")
	}
	preview, err := deps.RollbackEngine.DryRun(types.OpID(p.OpID))
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"preview": preview}, nil
}

// --- operations.rollback ---

type operationsRollbackPayload struct {
	OpID string `json:"opId"`
}

func operationsRollback(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.RollbackEngine == nil {
		return nil, fmt.Errorf("rollback engine not available")
	}
	var p operationsRollbackPayload
	if err := decodePayload(req.Payload, &p); err != nil || p.OpID == "" {
		return nil, fmt.Errorf("opId is required")
	}
	op, err := deps.RollbackEngine.Rollback(types.OpID(p.OpID))
	if err != nil {
		return nil, fmt.Errorf("rollback failed: %w", err)
	}
	return map[string]interface{}{
		"rolledBack": true,
		"opId":       op.OpID,
		"capability": op.Capability,
		"timestamp":  op.RolledBackAt,
	}, nil
}

// --- operations.rollbackRange ---

type operationsRollbackRangePayload struct {
	FromTime int64 `json:"fromTime"` // unix millis
	ToTime   int64 `json:"toTime"`   // unix millis
}

func operationsRollbackRange(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.RollbackEngine == nil {
		return nil, fmt.Errorf("rollback engine not available")
	}
	var p operationsRollbackRangePayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("fromTime and toTime are required")
	}
	if p.FromTime == 0 && p.ToTime == 0 {
		return nil, fmt.Errorf("fromTime and toTime are required")
	}
	rolledBack, err := deps.RollbackEngine.RollbackRange(
		time.UnixMilli(p.FromTime),
		time.UnixMilli(p.ToTime),
	)
	if err != nil {
		return nil, fmt.Errorf("range rollback failed: %w", err)
	}
	return map[string]interface{}{
		"count":      len(rolledBack),
		"rolledBack": rolledBack,
	}, nil
}

// --- operations.verify ---

func operationsVerify(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.RollbackEngine == nil {
		return nil, fmt.Errorf("rollback engine not available")
	}
	artifacts := deps.RollbackEngine.VerifyArtifacts()
	content := deps.RollbackEngine.VerifyContent()

	missingCount := 0
	corruptedCount := 0
	for _, r := range artifacts {
		switch r.Status {
		case "missing":
			missingCount++
		case "corrupted":
			corruptedCount++
		}
	}

	return map[string]interface{}{
		"artifacts":    artifacts,
		"content":      content,
		"totalChecked": len(artifacts) + len(content),
		"missing":      missingCount,
		"corrupted":    corruptedCount,
	}, nil
}
