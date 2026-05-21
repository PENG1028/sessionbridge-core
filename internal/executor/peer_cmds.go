package executor

import (
	"fmt"
	"net"

	"github.com/user/sessionnode/go-core/internal/mesh"
	"github.com/user/sessionnode/go-core/pkg/types"
)

type peerInfoPayload struct {
	NodeID string `json:"nodeId"`
}

// nodePeerList returns all trusted peers with runtime status cross-referenced
// from topology. Trust records come from the mesh TrustStore; connection status
// comes from the runtime topology (NodeLister).
func nodePeerList(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.Mesh == nil || deps.Mesh.TrustStore == nil {
		return map[string]interface{}{"peers": []interface{}{}}, nil
	}

	trustedPeers := deps.Mesh.TrustStore.List()

	// Build a lookup from topology for runtime connection status.
	runtimeStatus := make(map[string]string)
	if deps.Nodes != nil {
		for _, n := range deps.Nodes.ListNodes() {
			runtimeStatus[string(n.ID)] = n.Status
		}
	}

	peers := make([]map[string]interface{}, 0, len(trustedPeers))
	for _, tp := range trustedPeers {
		entry := map[string]interface{}{
			"nodeId":         string(tp.NodeID),
			"name":           tp.Name,
			"fingerprint":    tp.Fingerprint,
			"addresses":      tp.Addresses,
			"trustExpiresAt": tp.TrustExpiresAt,
			"autoReconnect":  tp.AutoReconnect,
			"lastSeen":       tp.LastSeen,
			"policy":         map[string]interface{}{"mode": tp.Policy.Mode},
		}

		// Determine status: runtime status takes priority; trust status overrides.
		status := "offline"
		if rs, ok := runtimeStatus[string(tp.NodeID)]; ok {
			switch rs {
			case "connected":
				status = "connected"
			case "connecting":
				status = "connecting"
			case "disconnected":
				status = "reconnecting"
			case "local":
				status = "connected"
			}
		}
		switch tp.Status {
		case mesh.TrustStatusRevoked:
			status = "revoked"
		case mesh.TrustStatusExpired:
			status = "expired"
		}
		entry["status"] = status

		peers = append(peers, entry)
	}

	return map[string]interface{}{"peers": peers}, nil
}

// nodePeerInfo returns detailed information about a single peer from the trust
// store, enriched with runtime connection status from topology.
func nodePeerInfo(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p peerInfoPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	if deps.Mesh == nil || deps.Mesh.TrustStore == nil {
		return nil, fmt.Errorf("trust store not available")
	}

	tp, err := deps.Mesh.TrustStore.Get(p.NodeID)
	if err != nil {
		return nil, fmt.Errorf("peer %q not found in trust store", p.NodeID)
	}

	// Cross-reference with runtime status from topology.
	runtimeStatus := "offline"
	if deps.Nodes != nil {
		for _, n := range deps.Nodes.ListNodes() {
			if string(n.ID) == p.NodeID {
				switch n.Status {
				case "connected", "local":
					runtimeStatus = "connected"
				case "connecting":
					runtimeStatus = "connecting"
				case "disconnected":
					runtimeStatus = "reconnecting"
				}
				break
			}
		}
	}

	status := runtimeStatus
	switch tp.Status {
	case mesh.TrustStatusRevoked:
		status = "revoked"
	case mesh.TrustStatusExpired:
		status = "expired"
	}

	return map[string]interface{}{
		"nodeId":         string(tp.NodeID),
		"name":           tp.Name,
		"fingerprint":    tp.Fingerprint,
		"addresses":      tp.Addresses,
		"status":         status,
		"lastSeen":       tp.LastSeen,
		"trustExpiresAt": tp.TrustExpiresAt,
		"autoReconnect":  tp.AutoReconnect,
		"policy":         map[string]interface{}{"mode": tp.Policy.Mode},
	}, nil
}

// nodePeerReconnect triggers a reconnection attempt to a peer.
// Topology auto-reconnects, so this signals intent and returns the current status.
func nodePeerReconnect(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p peerInfoPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	if deps.Mesh == nil || deps.Mesh.TrustStore == nil {
		return nil, fmt.Errorf("trust store not available")
	}

	if _, err := deps.Mesh.TrustStore.Get(p.NodeID); err != nil {
		return nil, fmt.Errorf("peer %q not found in trust store", p.NodeID)
	}

	return map[string]interface{}{
		"status": "reconnecting",
		"nodeId": p.NodeID,
	}, nil
}

// nodePeerDisconnect disconnects a peer connection but keeps the trust record.
// Full disconnect implementation requires topology to expose a DisconnectPeer method.
func nodePeerDisconnect(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p peerInfoPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	if deps.Mesh == nil || deps.Mesh.TrustStore == nil {
		return nil, fmt.Errorf("trust store not available")
	}

	if _, err := deps.Mesh.TrustStore.Get(p.NodeID); err != nil {
		return nil, fmt.Errorf("peer %q not found in trust store", p.NodeID)
	}

	// Full disconnect implementation requires topology to expose DisconnectPeer method.
	// For now, verify the peer exists in the trust store and return disconnected status.
	// The trust record is intentionally preserved.

	return map[string]interface{}{
		"status": "disconnected",
		"nodeId": p.NodeID,
	}, nil
}

// nodePeerRevoke removes a peer from the trust store.
// If the peer has an active connection, it should also be disconnected.
func nodePeerRevoke(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p peerInfoPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	if deps.Mesh == nil || deps.Mesh.TrustStore == nil {
		return nil, fmt.Errorf("trust store not available")
	}

	if err := deps.Mesh.TrustStore.Remove(p.NodeID); err != nil {
		return nil, fmt.Errorf("revoke peer: %w", err)
	}

	return map[string]interface{}{
		"status": "revoked",
		"nodeId": p.NodeID,
	}, nil
}

// nodeReachabilityCheck returns a minimal reachability assessment.
// MVP: checks whether the server is listening on a non-loopback address.
// No STUN or NAT detection is performed.
func nodeReachabilityCheck(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	inboundPeerAllowed := false
	reason := "peer WS endpoint is not listening on a non-loopback address"

	if deps.Config != nil {
		cfg := deps.Config.Get()
		addr := cfg.Core.ListenAddr
		if addr != "" {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				// addr might not have a port; try parsing as-is.
				host = addr
			}
			if host == "" || host == "0.0.0.0" || host == "::" {
				inboundPeerAllowed = true
				reason = fmt.Sprintf("peer WS endpoint is active, listening on %s", addr)
			} else if ip := net.ParseIP(host); ip != nil && !ip.IsLoopback() {
				inboundPeerAllowed = true
				reason = fmt.Sprintf("peer WS endpoint is active, listening on %s", addr)
			} else if ip != nil && ip.IsLoopback() {
				reason = fmt.Sprintf("listening on loopback %s, outbound only", addr)
			}
		}
	}

	return map[string]interface{}{
		"publicReachable":   "unknown",
		"inboundPeerAllowed": inboundPeerAllowed,
		"outboundOnly":      !inboundPeerAllowed,
		"reason":            reason,
	}, nil
}
