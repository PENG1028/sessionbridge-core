package executor

import (
	"encoding/hex"
	"fmt"
	"time"

	"github.com/user/sessionnode/go-core/internal/mesh"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// ---- payload types ----

type nodeIdentityGetPayload struct{}

type nodeInviteCreatePayload struct {
	TTLSeconds           int    `json:"ttlSeconds"`
	TrustDurationSeconds int64  `json:"trustDurationSeconds"`
	RoleHint             string `json:"roleHint,omitempty"`
	NameHint             string `json:"nameHint,omitempty"`
}

type nodeInviteRevokePayload struct {
	InviteID string `json:"inviteId"`
}

type nodeInviteAcceptPayload struct {
	PeerURL   string `json:"peerUrl"`
	Code      string `json:"code"`
	NameHint  string `json:"nameHint,omitempty"`
}

type nodeInviteListPayload struct{}

// ---- handlers ----

// nodeIdentityGet returns the public identity of this node.
// Does NOT return the private key.
func nodeIdentityGet(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.Mesh == nil || deps.Mesh.Identity == nil {
		return nil, &types.CoreError{Code: "NOT_FOUND", Message: "node identity not available"}
	}

	id := deps.Mesh.Identity

	return map[string]interface{}{
		"nodeId":      string(id.NodeID),
		"publicKey":   hex.EncodeToString(id.PublicKey),
		"fingerprint": id.Fingerprint,
		"createdAt":   id.CreatedAt,
	}, nil
}

// nodeInviteCreate generates a one-time invite code for peer pairing.
func nodeInviteCreate(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.Mesh == nil || deps.Mesh.Identity == nil {
		return nil, &types.CoreError{Code: "NOT_FOUND", Message: "node identity not available"}
	}
	if deps.Mesh.InviteStore == nil {
		return nil, &types.CoreError{Code: "NOT_FOUND", Message: "invite store not available"}
	}

	var p nodeInviteCreatePayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, &types.CoreError{Code: "INVALID_PAYLOAD", Message: fmt.Sprintf("invalid payload: %v", err)}
	}

	if p.TTLSeconds == 0 {
		p.TTLSeconds = 60
	}

	invite, err := deps.Mesh.InviteStore.Create(deps.Mesh.Identity, p.TTLSeconds, p.TrustDurationSeconds)
	if err != nil {
		return nil, &types.CoreError{Code: "INTERNAL", Message: fmt.Sprintf("failed to create invite: %v", err)}
	}

	return map[string]interface{}{
		"inviteId":             invite.InviteID,
		"code":                 invite.Code,
		"expiresAt":            invite.ExpiresAt,
		"trustDurationSeconds": invite.TrustDurationSeconds,
		"localNode": map[string]interface{}{
			"nodeId":      invite.LocalNodeID,
			"fingerprint": invite.LocalFingerprint,
			"publicKey":   hex.EncodeToString(invite.LocalPublicKey),
		},
	}, nil
}

// nodeInviteList returns all current non-expired invites (without codes).
func nodeInviteList(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.Mesh == nil || deps.Mesh.InviteStore == nil {
		return nil, &types.CoreError{Code: "NOT_FOUND", Message: "invite store not available"}
	}

	invites := deps.Mesh.InviteStore.List()
	out := make([]map[string]interface{}, 0, len(invites))
	for _, inv := range invites {
		out = append(out, map[string]interface{}{
			"inviteId":             inv.InviteID,
			"createdAt":            inv.CreatedAt,
			"expiresAt":            inv.ExpiresAt,
			"ttlSeconds":           inv.TTLSeconds,
			"trustDurationSeconds": inv.TrustDurationSeconds,
			"localNodeId":          inv.LocalNodeID,
			"localFingerprint":     inv.LocalFingerprint,
		})
	}

	return map[string]interface{}{
		"invites": out,
		"total":   len(out),
	}, nil
}

// nodeInviteRevoke removes an invite by ID.
func nodeInviteRevoke(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.Mesh == nil || deps.Mesh.InviteStore == nil {
		return nil, &types.CoreError{Code: "NOT_FOUND", Message: "invite store not available"}
	}

	var p nodeInviteRevokePayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, &types.CoreError{Code: "INVALID_PAYLOAD", Message: fmt.Sprintf("invalid payload: %v", err)}
	}
	if p.InviteID == "" {
		return nil, &types.CoreError{Code: "MISSING_FIELD", Message: "inviteId is required"}
	}

	if err := deps.Mesh.InviteStore.Revoke(p.InviteID); err != nil {
		return nil, &types.CoreError{Code: "NOT_FOUND", Message: err.Error()}
	}

	return map[string]interface{}{
		"ok":       true,
		"inviteId": p.InviteID,
	}, nil
}

// nodeInviteAccept validates an invite code and stores the peer in the trust store.
// Phase 1: validates locally, adds peer with "pending" trust status.
// Phase 2 (Agent C): actual WS connection will be established by topology.
func nodeInviteAccept(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.Mesh == nil || deps.Mesh.InviteStore == nil {
		return nil, &types.CoreError{Code: "NOT_FOUND", Message: "invite store not available"}
	}
	if deps.Mesh.TrustStore == nil {
		return nil, &types.CoreError{Code: "NOT_FOUND", Message: "trust store not available"}
	}

	var p nodeInviteAcceptPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, &types.CoreError{Code: "INVALID_PAYLOAD", Message: fmt.Sprintf("invalid payload: %v", err)}
	}
	if p.Code == "" {
		return nil, &types.CoreError{Code: "MISSING_FIELD", Message: "code is required"}
	}

	// Phase 1: Validate the invite code locally
	invite, err := deps.Mesh.InviteStore.Validate(p.Code)
	if err != nil {
		return nil, &types.CoreError{Code: "INVALID_INVITE", Message: fmt.Sprintf("invite validation failed: %v", err)}
	}

	peerName := p.NameHint
	if peerName == "" {
		peerName = invite.LocalNodeID
	}

	// Determine trust expiration
	var trustExpiresAt int64
	if invite.TrustDurationSeconds > 0 {
		trustExpiresAt = time.Now().Unix() + invite.TrustDurationSeconds
	}
	// 0 = permanent

	// Phase 2: Store the peer in the trust store with "pending" trust
	// The actual WS connection will be established by topology (Agent C)
	peer := &mesh.TrustedPeer{
		NodeID:         invite.LocalNodeID,
		Name:           peerName,
		PublicKey:      invite.LocalPublicKey,
		Fingerprint:    invite.LocalFingerprint,
		Addresses:      []string{p.PeerURL},
		TrustExpiresAt: trustExpiresAt,
		AutoReconnect:  false,
		Status:         "pending",
		LastSeen:       time.Now().Unix(),
		Policy:         mesh.TrustPolicy{Mode: "full"},
	}

	if err := deps.Mesh.TrustStore.Add(peer); err != nil {
		return nil, &types.CoreError{Code: "INTERNAL", Message: fmt.Sprintf("failed to store peer: %v", err)}
	}

	return map[string]interface{}{
		"status": "accepted",
		"peer": map[string]interface{}{
			"nodeId":         peer.NodeID,
			"fingerprint":    peer.Fingerprint,
			"addresses":      peer.Addresses,
			"trustExpiresAt": peer.TrustExpiresAt,
			"policy": map[string]interface{}{
				"mode": peer.Policy.Mode,
			},
		},
	}, nil
}
