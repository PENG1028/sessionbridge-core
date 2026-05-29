package executor

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
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
	PeerURL  string `json:"peerUrl"`
	Code     string `json:"code"`
	NameHint string `json:"nameHint,omitempty"`
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

// peerURLToInviteAcceptURL converts a WebSocket URL to an HTTP invite-accept URL.
// ws://host:port/path -> http://host:port/peer/invite/accept
// wss://host:port/path -> https://host:port/peer/invite/accept
func peerURLToInviteAcceptURL(peerURL string) string {
	rest := strings.TrimPrefix(peerURL, "ws://")
	if rest != peerURL {
		return "http://" + strings.Split(rest, "/")[0] + "/peer/invite/accept"
	}
	rest = strings.TrimPrefix(peerURL, "wss://")
	if rest != peerURL {
		return "https://" + strings.Split(rest, "/")[0] + "/peer/invite/accept"
	}
	// Plain host:port -- assume ws
	return "http://" + peerURL + "/peer/invite/accept"
}

// normalizePeerAddress extracts a clean host:port from a WebSocket URL for trust store storage.
func normalizePeerAddress(peerURL string) string {
	rest := strings.TrimPrefix(peerURL, "ws://")
	if rest != peerURL {
		return strings.Split(rest, "/")[0]
	}
	rest = strings.TrimPrefix(peerURL, "wss://")
	if rest != peerURL {
		return strings.Split(rest, "/")[0]
	}
	return peerURL
}

// nodeInviteAccept validates an invite code via the remote peer's HTTP endpoint
// and stores the remote peer in the local trust store.
//
// Flow:
//  1. Caller sends node.invite.accept with {peerUrl, code, nameHint}
//  2. This function makes an HTTP POST to peerUrl/peer/invite/accept
//     with the caller's identity (nodeId, publicKey, fingerprint)
//  3. Remote peer validates the invite code (one-time use via Consume),
//     stores the caller in its trust store, returns remote identity
//  4. This function stores the remote peer in the local trust store
//  5. Topology is signaled to connect to the new peer
func nodeInviteAccept(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.Mesh == nil || deps.Mesh.Identity == nil {
		return nil, &types.CoreError{Code: "NOT_FOUND", Message: "node identity not available"}
	}
	if deps.Mesh.InviteStore == nil {
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
	if p.PeerURL == "" {
		return nil, &types.CoreError{Code: "MISSING_FIELD", Message: "peerUrl is required"}
	}

	// Build the HTTP invite-accept URL from the peer's WebSocket URL.
	inviteURL := peerURLToInviteAcceptURL(p.PeerURL)

	// Prepare the request body with our identity.
	body := map[string]interface{}{
		"code":        p.Code,
		"nodeId":      string(deps.Mesh.Identity.NodeID),
		"publicKey":   hex.EncodeToString(deps.Mesh.Identity.PublicKey),
		"fingerprint": deps.Mesh.Identity.Fingerprint,
		"addressHint": normalizePeerAddress(p.PeerURL),
	}
	if p.NameHint != "" {
		body["nameHint"] = p.NameHint
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, &types.CoreError{Code: "INTERNAL", Message: fmt.Sprintf("failed to marshal request: %v", err)}
	}

	// Send HTTP POST to the remote peer's invite-accept endpoint.
	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Post(inviteURL, "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, &types.CoreError{Code: "CONNECTION_FAILED", Message: fmt.Sprintf("failed to contact peer at %s: %v", inviteURL, err)}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &types.CoreError{Code: "INTERNAL", Message: fmt.Sprintf("failed to read peer response: %v", err)}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &types.CoreError{Code: "PEER_REJECTED", Message: fmt.Sprintf("peer rejected invite (HTTP %d): %s", resp.StatusCode, string(respBody))}
	}

	// Parse the remote peer's response to get their identity.
	// The response uses a "node" field (not "peer") - see handlePeerInviteAccept in server.go.
	var remoteResp struct {
		Status string `json:"status"`
		Node   struct {
			NodeID      string `json:"nodeId"`
			PublicKey   string `json:"publicKey"`
			Fingerprint string `json:"fingerprint"`
		} `json:"node"`
		TrustExpiresAt int64  `json:"trustExpiresAt"`
		PeerWSPath     string `json:"peerWsPath"`
	}
	if err := json.Unmarshal(respBody, &remoteResp); err != nil {
		return nil, &types.CoreError{Code: "INTERNAL", Message: fmt.Sprintf("failed to decode peer response: %v", err)}
	}

	if remoteResp.Status != "accepted" {
		return nil, &types.CoreError{Code: "PEER_REJECTED", Message: fmt.Sprintf("peer did not accept: %s", remoteResp.Status)}
	}

	remoteNodeID := remoteResp.Node.NodeID
	remotePubKeyHex := remoteResp.Node.PublicKey
	remoteFingerprint := remoteResp.Node.Fingerprint

	// Reject self-pairing — a node must never be its own peer.
	if remoteNodeID == string(deps.Mesh.Identity.NodeID) {
		return nil, &types.CoreError{Code: "INVALID_RESPONSE", Message: "remote peer returned our own nodeId — refusing to pair with self"}
	}

	// Validate the response fields.
	if remoteNodeID == "" {
		return nil, &types.CoreError{Code: "INVALID_RESPONSE", Message: "remote peer returned empty nodeId"}
	}
	if remotePubKeyHex == "" {
		return nil, &types.CoreError{Code: "INVALID_RESPONSE", Message: "remote peer returned empty publicKey"}
	}
	if remoteFingerprint == "" {
		return nil, &types.CoreError{Code: "INVALID_RESPONSE", Message: "remote peer returned empty fingerprint"}
	}

	remotePubKey, err := hex.DecodeString(remotePubKeyHex)
	if err != nil {
		return nil, &types.CoreError{Code: "INVALID_RESPONSE", Message: fmt.Sprintf("remote peer returned invalid publicKey hex: %v", err)}
	}
	if len(remotePubKey) != ed25519.PublicKeySize {
		return nil, &types.CoreError{Code: "INVALID_RESPONSE", Message: fmt.Sprintf("remote peer publicKey has wrong length: got %d, want %d", len(remotePubKey), ed25519.PublicKeySize)}
	}

	// Build address list from the peer URL.
	remoteAddresses := []string{normalizePeerAddress(p.PeerURL)}
	if remoteResp.PeerWSPath != "" && remoteResp.PeerWSPath != "/peer/ws" {
		scheme := "ws://"
		if strings.HasPrefix(p.PeerURL, "wss://") {
			scheme = "wss://"
		}
		baseHost := normalizePeerAddress(p.PeerURL)
		remoteAddresses = []string{scheme + baseHost + remoteResp.PeerWSPath, baseHost}
	}

	peerName := p.NameHint
	if peerName == "" {
		peerName = remoteNodeID
	}

	// Build the trusted peer with the remote node's public key.
	peer := &mesh.TrustedPeer{
		NodeID:         remoteNodeID,
		Name:           peerName,
		PublicKey:      remotePubKey,
		Fingerprint:    remoteFingerprint,
		Addresses:      remoteAddresses,
		TrustExpiresAt: remoteResp.TrustExpiresAt,
		AutoReconnect:  true,
		Status:         mesh.TrustStatusOffline,
		LastSeen:       time.Now().UnixMilli(),
		Policy:         mesh.TrustPolicy{Mode: "full"},
	}

	// Store the remote peer in our trust store.
	// TrustStore.Add is insert-or-update, so re-accepting the same peer works.
	if err := deps.Mesh.TrustStore.Add(peer); err != nil {
		return nil, &types.CoreError{Code: "INTERNAL", Message: fmt.Sprintf("failed to store peer: %v", err)}
	}

	// Signal topology to add and connect to the peer.
	if deps.Topology != nil {
		if err := deps.Topology.AddOrUpdatePeer(types.NodeID(remoteNodeID), remoteAddresses[0], true); err != nil {
			log.Printf("[mesh] invite accept: AddOrUpdatePeer %s: %v", remoteNodeID, err)
		}
		if err := deps.Topology.ConnectPeer(types.NodeID(remoteNodeID)); err != nil {
			log.Printf("[mesh] invite accept: ConnectPeer %s: %v", remoteNodeID, err)
		}
	}

	// Include publicKey in the response for verification by UI/test.
	return map[string]interface{}{
		"status": "accepted",
		"peer": map[string]interface{}{
			"nodeId":         peer.NodeID,
			"publicKey":      remotePubKeyHex,
			"fingerprint":    peer.Fingerprint,
			"addresses":      peer.Addresses,
			"trustExpiresAt": peer.TrustExpiresAt,
			"policy": map[string]interface{}{
				"mode": peer.Policy.Mode,
			},
		},
	}, nil
}
