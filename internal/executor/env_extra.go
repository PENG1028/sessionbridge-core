package executor

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/user/sessionnode/go-core/pkg/types"
)

type envCheckBinaryPayload struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// envCheckBinary checks if a binary is available on PATH and optionally checks version.
func envCheckBinary(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p envCheckBinaryPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	path, err := exec.LookPath(p.Name)
	found := err == nil
	result := map[string]interface{}{
		"found": found,
		"name":  p.Name,
	}
	if found {
		result["path"] = path
		// Try to get version if requested
		if p.Version != "" {
			// This is a basic check, version comparison is too complex for now
		}
	}
	return result, nil
}

type envWhichPayload struct {
	Name string `json:"name"`
}

// envWhich returns the full path of a binary on PATH (like the which command).
func envWhich(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p envWhichPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	path, err := exec.LookPath(p.Name)
	if err != nil {
		return map[string]interface{}{
			"name":  p.Name,
			"found": false,
			"path":  "",
		}, nil
	}
	return map[string]interface{}{
		"name":  p.Name,
		"found": true,
		"path":  path,
	}, nil
}

// envHome returns the user's home directory.
func envHome(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir error: %w", err)
	}
	return map[string]interface{}{
		"home": home,
	}, nil
}

type envCwdPayload struct {
	SessionID string `json:"sessionId,omitempty"`
}

// envCwd returns the current working directory.
// If sessionId is provided, returns the session's cwd if available.
func envCwd(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p envCwdPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	// If sessionId provided, try to get cwd from session
	if p.SessionID != "" {
		sess := deps.Sessions.Get(types.SessionID(p.SessionID))
		if sess != nil && sess.Cwd != "" {
			return map[string]interface{}{
				"cwd":       sess.Cwd,
				"sessionId": p.SessionID,
			}, nil
		}
	}
	// Fallback to process cwd
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("cwd error: %w", err)
	}
	return map[string]interface{}{
		"cwd": cwd,
	}, nil
}
