package executor

import (
	"encoding/json"

	"github.com/user/sessionnode/go-core/pkg/types"
)

// taskList returns all tasks currently tracked in the TaskStore.
func taskList(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.TaskStore == nil {
		return map[string]interface{}{"tasks": []interface{}{}}, nil
	}
	return map[string]interface{}{"tasks": deps.TaskStore.List()}, nil
}

// taskInfo returns a single task by taskId from the request payload.
func taskInfo(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var payload struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		return nil, &types.CoreError{Code: "INVALID_PAYLOAD", Message: "invalid payload: " + err.Error()}
	}
	if payload.TaskID == "" {
		return nil, &types.CoreError{Code: "MISSING_FIELD", Message: "taskId is required"}
	}
	if deps.TaskStore == nil {
		return nil, &types.CoreError{Code: "NOT_FOUND", Message: "task not found"}
	}
	t, ok := deps.TaskStore.Get(payload.TaskID)
	if !ok {
		return nil, &types.CoreError{Code: "NOT_FOUND", Message: "task not found"}
	}
	return t, nil
}
