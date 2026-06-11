package server

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/PENG1028/sessionbridge-core/internal/mesh"
)

// handlePeerRegister handles POST /peer/register — auto-registration endpoint.
//
// Request:
//
//	{
//	  "nodeId":       "abc...",
//	  "publicKey":    "hex-ed25519-pubkey",
//	  "fingerprint":  "sha256-of-pubkey",
//	  "mode":         "transit" | "full"
//	}
//
// Responses:
//
//	200: {"status":"registered","mode":"transit","nodeId":"hub-...",...}
//	400: {"error":"..."} — bad request
//	403: {"error":"..."} — rejected by policy
func (s *Server) handlePeerRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	if s.identity == nil || s.trustStore == nil {
		http.Error(w, `{"error":"server identity or trust store not configured"}`, http.StatusInternalServerError)
		return
	}

	var req struct {
		NodeID      string `json:"nodeId"`
		PublicKey   string `json:"publicKey"`
		Fingerprint string `json:"fingerprint"`
		Mode        string `json:"mode,omitempty"`
		Token       string `json:"token,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.NodeID == "" || req.PublicKey == "" || req.Fingerprint == "" {
		http.Error(w, `{"error":"nodeId, publicKey, and fingerprint are required"}`, http.StatusBadRequest)
		return
	}

	// Reject self-registration
	if req.NodeID == s.identity.NodeID {
		http.Error(w, `{"error":"cannot register yourself"}`, http.StatusBadRequest)
		return
	}

	// Validate public key format
	pubKeyBytes, err := hex.DecodeString(req.PublicKey)
	if err != nil || len(pubKeyBytes) != ed25519.PublicKeySize {
		http.Error(w, `{"error":"invalid public key"}`, http.StatusBadRequest)
		return
	}

	// Resolve trust mode based on policy
	mode := s.resolveRegisterMode(req.Mode, req.Token)
	if mode == "" {
		http.Error(w, `{"error":"registration rejected by policy"}`, http.StatusForbidden)
		return
	}

	// Resolve peer addresses from request
	addresses := resolveRegisterAddresses(r)

	// Check if peer already exists
	existing, err := s.trustStore.Get(req.NodeID)
	if err == nil && existing != nil {
		// Update addresses
		_ = s.trustStore.UpdatePeer(req.NodeID, func(p *mesh.TrustedPeer) {
			p.Addresses = addresses
			p.LastSeen = time.Now().UnixMilli()
			p.Status = mesh.TrustStatusOffline
		})
		log.Printf("[register] updated peer %s (mode=%s)", req.NodeID, mode)
	} else {
		// Create new trusted peer
		peer := &mesh.TrustedPeer{
			NodeID:        req.NodeID,
			Name:          req.NodeID,
			PublicKey:     pubKeyBytes,
			Fingerprint:   req.Fingerprint,
			Addresses:     addresses,
			AutoReconnect: true,
			Status:        mesh.TrustStatusOffline,
			LastSeen:      time.Now().UnixMilli(),
			Policy:        mesh.TrustPolicy{Mode: mode},
		}
		if err := s.trustStore.Add(peer); err != nil {
			log.Printf("[register] failed to store peer %s: %v", req.NodeID, err)
			http.Error(w, `{"error":"failed to store peer"}`, http.StatusInternalServerError)
			return
		}
		log.Printf("[register] new peer %s registered (mode=%s)", req.NodeID, mode)
	}

	resp := map[string]interface{}{
		"status":      "registered",
		"mode":        mode,
		"nodeId":      s.identity.NodeID,
		"publicKey":   hex.EncodeToString(s.identity.PublicKey),
		"fingerprint": s.identity.Fingerprint,
		"peerWsPath":  "/peer/ws",
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// resolveRegisterMode decides the trust mode for a registering peer.
func (s *Server) resolveRegisterMode(requestedMode, token string) string {
	policy := s.registerPolicy
	if policy == "" {
		policy = "open" // default
	}

	switch policy {
	case "open":
		// Anyone can register. Requested mode is honored.
		if requestedMode == "full" {
			return "full"
		}
		return "transit"

	case "token":
		if token == "" || token != s.registerToken {
			return "" // rejected
		}
		if requestedMode == "transit" {
			return "transit"
		}
		return "full"

	case "manual":
		// Always transit pending admin approval
		return "transit"

	default:
		return "transit"
	}
}

// resolveRegisterAddresses extracts peer addresses from the HTTP request.
func resolveRegisterAddresses(r *http.Request) []string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" {
		return nil
	}
	// Assume default Core port
	return []string{fmt.Sprintf("%s:9090", host)}
}

// RegisterPolicy returns the configured registration policy.
func (s *Server) RegisterPolicy() string {
	if s.registerPolicy == "" {
		return "open"
	}
	return s.registerPolicy
}
