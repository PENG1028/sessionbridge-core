package executor

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/user/sessionnode/go-core/pkg/types"
)

type fsReadPayload struct {
	Path string `json:"path"`
}

func fsRead(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p fsReadPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.Path == "" {
		return nil, fmt.Errorf("path is required")
	}
	data, err := os.ReadFile(p.Path)
	if err != nil {
		return nil, fmt.Errorf("read error: %w", err)
	}
	return map[string]interface{}{
		"path": p.Path,
		"size": len(data),
		"data": string(data),
	}, nil
}

type fsWritePayload struct {
	Path string `json:"path"`
	Data string `json:"data"`
}

func fsWrite(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p fsWritePayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.Path == "" {
		return nil, fmt.Errorf("path is required")
	}
	if err := os.WriteFile(p.Path, []byte(p.Data), 0644); err != nil {
		return nil, fmt.Errorf("write error: %w", err)
	}
	return map[string]interface{}{
		"path":   p.Path,
		"written": len(p.Data),
	}, nil
}

type fsListPayload struct {
	Path string `json:"path"`
}

func fsList(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p fsListPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.Path == "" {
		p.Path = "."
	}
	entries, err := os.ReadDir(p.Path)
	if err != nil {
		return nil, fmt.Errorf("list error: %w", err)
	}
	out := make([]map[string]interface{}, 0, len(entries))
	for _, e := range entries {
		info, _ := e.Info()
		size := int64(0)
		mode := ""
		if info != nil {
			size = info.Size()
			mode = info.Mode().String()
		}
		out = append(out, map[string]interface{}{
			"name":  e.Name(),
			"isDir": e.IsDir(),
			"size":  size,
			"mode":  mode,
		})
	}
	return map[string]interface{}{
		"path":    p.Path,
		"entries": out,
	}, nil
}

// resolvePath resolves a relative path against the given base, returning an absolute path.
func resolvePath(path, base string) (string, error) {
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	abs, err := filepath.Abs(filepath.Join(base, path))
	if err != nil {
		return "", err
	}
	return abs, nil
}
