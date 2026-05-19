package executor

import (
	"fmt"
	"os"

	"github.com/user/sessionnode/go-core/pkg/types"
)

type fsStatPayload struct {
	Path string `json:"path"`
}

// fsStat returns file/directory metadata for the given path.
func fsStat(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p fsStatPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.Path == "" {
		return nil, fmt.Errorf("path is required")
	}
	info, err := os.Stat(p.Path)
	if err != nil {
		return nil, fmt.Errorf("stat error: %w", err)
	}
	return map[string]interface{}{
		"name":    info.Name(),
		"size":    info.Size(),
		"mode":    info.Mode().String(),
		"modTime": info.ModTime().UnixMilli(),
		"isDir":   info.IsDir(),
	}, nil
}
