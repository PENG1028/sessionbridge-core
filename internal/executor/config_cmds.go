package executor

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/user/sessionnode/go-core/internal/config"
	"github.com/user/sessionnode/go-core/pkg/types"
)

type configGetPayload struct {
	Key string `json:"key"`
}

type configListPayload struct {
	Namespace string `json:"namespace,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
}

type configSetPayload struct {
	Key              string      `json:"key"`
	Value            interface{} `json:"value"`
	ExpectedRevision int64       `json:"expectedRevision,omitempty"`
}

type configResetPayload struct {
	Key              string `json:"key"`
	ExpectedRevision int64  `json:"expectedRevision,omitempty"`
}

func configList(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.Config == nil {
		return map[string]interface{}{"configs": []interface{}{}}, nil
	}

	var p configListPayload
	_ = decodePayload(req.Payload, &p)
	prefix := p.Prefix
	if prefix == "" {
		prefix = p.Namespace
	}

	entries, err := flattenConfig(deps.Config.Get(), prefix)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"configs": entries}, nil
}

func configGet(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.Config == nil {
		return nil, fmt.Errorf("config manager not available")
	}
	var p configGetPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.Key == "" {
		return nil, fmt.Errorf("key is required")
	}
	if isSensitiveConfigKey(p.Key) {
		return nil, fmt.Errorf("config key %q is sensitive and cannot be read", p.Key)
	}
	value, err := deps.Config.Value(p.Key)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"key": p.Key, "value": value, "revision": deps.Config.Get().Revision}, nil
}

func configSet(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.Config == nil {
		return nil, fmt.Errorf("config manager not available")
	}
	var p configSetPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.Key == "" {
		return nil, fmt.Errorf("key is required")
	}
	if err := deps.Config.SetWithRevision(p.Key, p.Value, p.ExpectedRevision); err != nil {
		if conflict, ok := err.(*config.ConfigConflictError); ok {
			return nil, &types.CoreError{
				Code:    "CONFIG_CONFLICT",
				Message: conflict.Error(),
			}
		}
		return nil, err
	}
	value, err := deps.Config.Value(p.Key)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"key": p.Key, "value": value, "revision": deps.Config.Get().Revision}, nil
}

func configReset(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.Config == nil {
		return nil, fmt.Errorf("config manager not available")
	}
	var p configResetPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.Key == "" {
		return nil, fmt.Errorf("key is required")
	}
	if err := deps.Config.ResetWithRevision(p.Key, p.ExpectedRevision); err != nil {
		if conflict, ok := err.(*config.ConfigConflictError); ok {
			return nil, &types.CoreError{
				Code:    "CONFIG_CONFLICT",
				Message: conflict.Error(),
			}
		}
		return nil, err
	}
	value, err := deps.Config.Value(p.Key)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"key": p.Key, "value": value, "revision": deps.Config.Get().Revision}, nil
}

func flattenConfig(cfg config.Config, prefix string) ([]map[string]interface{}, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	var root map[string]interface{}
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	revision := cfg.Revision
	delete(root, "_revision")
	removeSensitiveConfigValues(root)

	var entries []map[string]interface{}
	flattenConfigMap("", root, revision, prefix, &entries)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i]["key"].(string) < entries[j]["key"].(string)
	})
	return entries, nil
}

func flattenConfigMap(base string, value interface{}, revision int64, prefix string, out *[]map[string]interface{}) {
	if object, ok := value.(map[string]interface{}); ok {
		for key, child := range object {
			next := key
			if base != "" {
				next = base + "." + key
			}
			flattenConfigMap(next, child, revision, prefix, out)
		}
		return
	}
	if prefix != "" && base != prefix && (len(base) <= len(prefix) || base[:len(prefix)] != prefix) {
		return
	}
	*out = append(*out, map[string]interface{}{"key": base, "value": value, "revision": revision})
}

func removeSensitiveConfigValues(root map[string]interface{}) {
	core, ok := root["core"].(map[string]interface{})
	if !ok {
		return
	}
	auth, ok := core["auth"].(map[string]interface{})
	if !ok {
		return
	}
	delete(auth, "adminToken")
}

func isSensitiveConfigKey(key string) bool {
	return key == "core.auth.adminToken"
}
