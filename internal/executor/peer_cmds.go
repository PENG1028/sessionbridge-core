package executor

import (
	"fmt"
	"log"
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

	tp, err := deps.Mesh.TrustStore.Get(p.NodeID)
	if err != nil {
		return nil, fmt.Errorf("peer %q not found in trust store", p.NodeID)
	}
	if tp.Status == mesh.TrustStatusRevoked || tp.Status == mesh.TrustStatusExpired {
		return nil, fmt.Errorf("peer %q is %s, cannot reconnect", p.NodeID, tp.Status)
	}

	// Signal topology to reconnect
	if deps.Topology != nil {
		if err := deps.Topology.ReconnectPeer(types.NodeID(p.NodeID)); err != nil {
			log.Printf("[peer] reconnect %s: topology returned %v", p.NodeID, err)
		}
	}

	// Persist reconnect: enable auto-reconnect
	if err := deps.Mesh.TrustStore.UpdatePeer(p.NodeID, func(tp *mesh.TrustedPeer) {
		tp.AutoReconnect = true
	}); err != nil {
		log.Printf("[peer] reconnect %s: update trust store: %v", p.NodeID, err)
	}

	return map[string]interface{}{
		"status": "reconnecting",
		"nodeId": p.NodeID,
	}, nil
}

// nodePeerDisconnect disconnects a peer connection but keeps the trust record.
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

	// Signal topology to disconnect
	if deps.Topology != nil {
		if err := deps.Topology.DisconnectPeer(types.NodeID(p.NodeID)); err != nil {
			log.Printf("[peer] disconnect %s: topology returned %v", p.NodeID, err)
		}
	}

	// Persist the disconnect: disable auto-reconnect
	if err := deps.Mesh.TrustStore.UpdatePeer(p.NodeID, func(tp *mesh.TrustedPeer) {
		tp.AutoReconnect = false
		tp.Status = mesh.TrustStatusOffline
	}); err != nil {
		log.Printf("[peer] disconnect %s: update trust store: %v", p.NodeID, err)
	}

	return map[string]interface{}{
		"status": "disconnected",
		"nodeId": p.NodeID,
	}, nil
}

// nodePeerRevoke removes a peer from the trust store and topology.
func nodePeerRevoke(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p peerInfoPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	if deps.Mesh == nil || deps.Mesh.TrustStore == nil {
		return nil, fmt.Errorf("trust store not available")
	}

	// Remove from topology first (best-effort)
	if deps.Topology != nil {
		if err := deps.Topology.RemovePeer(types.NodeID(p.NodeID)); err != nil {
			log.Printf("[peer] revoke %s: topology remove returned %v", p.NodeID, err)
		}
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
		"publicReachable":    "unknown",
		"inboundPeerAllowed": inboundPeerAllowed,
		"outboundOnly":       !inboundPeerAllowed,
		"reason":             reason,
	}, nil
}
