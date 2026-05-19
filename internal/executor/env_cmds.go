package executor

import (
	"fmt"
	"os"
	"strings"

	"github.com/user/sessionnode/go-core/pkg/types"
)

type envGetPayload struct {
	Name string `json:"name"`
}

func envGet(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p envGetPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	val := os.Getenv(p.Name)
	found := val != ""
	return map[string]interface{}{
		"name":  p.Name,
		"value": val,
		"found": found,
	}, nil
}

type envSetPayload struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func envSet(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p envSetPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if err := os.Setenv(p.Name, p.Value); err != nil {
		return nil, fmt.Errorf("setenv error: %w", err)
	}
	return map[string]interface{}{
		"name":  p.Name,
		"value": p.Value,
	}, nil
}

func envList(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	env := os.Environ()
	out := make(map[string]string)
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		out[k] = v
	}
	return map[string]interface{}{
		"count": len(out),
		"env":   out,
	}, nil
}

func envUnset(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p envGetPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if err := os.Unsetenv(p.Name); err != nil {
		return nil, fmt.Errorf("unsetenv error: %w", err)
	}
	return map[string]interface{}{
		"name": p.Name,
	}, nil
}
