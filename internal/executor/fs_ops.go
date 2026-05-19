package executor

import (
	"fmt"
	"os"

	"github.com/user/sessionnode/go-core/pkg/types"
)

type fsMkdirPayload struct {
	Path string `json:"path"`
	All  bool   `json:"all,omitempty"` // like mkdir -p
	Mode int    `json:"mode,omitempty"`
}

func fsMkdir(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p fsMkdirPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.Path == "" {
		return nil, fmt.Errorf("path is required")
	}
	mode := os.FileMode(p.Mode)
	if mode == 0 {
		mode = 0755
	}
	if p.All {
		if err := os.MkdirAll(p.Path, mode); err != nil {
			return nil, fmt.Errorf("mkdirall error: %w", err)
		}
	} else {
		if err := os.Mkdir(p.Path, mode); err != nil {
			return nil, fmt.Errorf("mkdir error: %w", err)
		}
	}
	return map[string]interface{}{
		"path": p.Path,
	}, nil
}

type fsRemovePayload struct {
	Path  string `json:"path"`
	Recursive bool `json:"recursive,omitempty"` // like rm -rf
}

func fsRemove(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p fsRemovePayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.Path == "" {
		return nil, fmt.Errorf("path is required")
	}
	if p.Recursive {
		if err := os.RemoveAll(p.Path); err != nil {
			return nil, fmt.Errorf("removeall error: %w", err)
		}
	} else {
		if err := os.Remove(p.Path); err != nil {
			return nil, fmt.Errorf("remove error: %w", err)
		}
	}
	return map[string]interface{}{
		"path": p.Path,
	}, nil
}

type fsRenamePayload struct {
	OldPath string `json:"oldPath"`
	NewPath string `json:"newPath"`
}

func fsRename(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p fsRenamePayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.OldPath == "" || p.NewPath == "" {
		return nil, fmt.Errorf("oldPath and newPath are required")
	}
	if err := os.Rename(p.OldPath, p.NewPath); err != nil {
		return nil, fmt.Errorf("rename error: %w", err)
	}
	return map[string]interface{}{
		"oldPath": p.OldPath,
		"newPath": p.NewPath,
	}, nil
}
