package executor

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/user/sessionnode/go-core/pkg/types"
)

type nodeInfoPayload struct {
	NodeID string `json:"nodeId"`
}

// nodeList returns all known nodes from the topology.
func nodeList(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.Nodes == nil {
		// Fallback if no topology is configured
		hostname, _ := os.Hostname()
		return map[string]interface{}{
			"nodes": []map[string]interface{}{
				{
					"nodeId":   "local",
					"hostname": hostname,
					"status":   "connected",
					"role":     "standalone",
				},
			},
		}, nil
	}

	nodes := deps.Nodes.ListNodes()
	out := make([]map[string]interface{}, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, map[string]interface{}{
			"nodeId":      string(n.ID),
			"name":        n.Name,
			"address":     n.Address,
			"tags":        n.Tags,
			"status":      n.Status,
			"displayName": n.DisplayName,
		})
	}
	return map[string]interface{}{
		"nodes": out,
		"total": len(out),
	}, nil
}

// nodeInfo returns detailed info about a specific node.
func nodeInfo(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p nodeInfoPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	if deps.Nodes != nil {
		for _, n := range deps.Nodes.ListNodes() {
			if string(n.ID) == p.NodeID || p.NodeID == "" {
				return enrichNodeInfo(n), nil
			}
		}
	}

	// Fallback: local-only
	hostname, _ := os.Hostname()
	return map[string]interface{}{
		"nodeId":   p.NodeID,
		"hostname": hostname,
		"status":   "unknown",
		"role":     "standalone",
		"os":       runtime.GOOS,
		"arch":     runtime.GOARCH,
		"numCPU":   runtime.NumCPU(),
	}, nil
}

func enrichNodeInfo(n NodeInfo) map[string]interface{} {
	hostname, _ := os.Hostname()
	cwd, _ := os.Getwd()
	return map[string]interface{}{
		"nodeId":      string(n.ID),
		"name":        n.Name,
		"address":     n.Address,
		"tags":        n.Tags,
		"status":      n.Status,
		"displayName": n.DisplayName,
		"hostname":    hostname,
		"cwd":         cwd,
		"os":          runtime.GOOS,
		"arch":        runtime.GOARCH,
		"numCPU":      runtime.NumCPU(),
	}
}

// nodeHealth returns health check for a node (local-only stub).
func nodeHealth(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	return map[string]interface{}{
		"nodeId": "local",
		"status": "ok",
		"time":   time.Now().UnixMilli(),
	}, nil
}
