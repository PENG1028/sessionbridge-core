package mesh

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"time"
)

// NodeCertificate is an ed25519-signed credential issued by a Relay CA.
// It authorizes a node to connect to other nodes that trust the CA.
type NodeCertificate struct {
	NodeID    string `json:"nodeId"`
	PublicKey []byte `json:"publicKey"`
	IssuedBy  string `json:"issuedBy"`
	IssuedAt  int64  `json:"issuedAt"`
	ExpiresAt int64  `json:"expiresAt"`
	Mode      string `json:"mode"`    // "full" or "transit"
	Signature []byte `json:"signature"`
}

// serializedForSigning returns the canonical bytes to sign/verify.
// This is just the JSON without the signature field, deterministic via json.Marshal.
func (c *NodeCertificate) serializedForSigning() ([]byte, error) {
	// Create a copy without Signature for signing
	stripped := struct {
		NodeID    string `json:"nodeId"`
		PublicKey []byte `json:"publicKey"`
		IssuedBy  string `json:"issuedBy"`
		IssuedAt  int64  `json:"issuedAt"`
		ExpiresAt int64  `json:"expiresAt"`
		Mode      string `json:"mode"`
	}{
		NodeID:    c.NodeID,
		PublicKey: c.PublicKey,
		IssuedBy:  c.IssuedBy,
		IssuedAt:  c.IssuedAt,
		ExpiresAt: c.ExpiresAt,
		Mode:      c.Mode,
	}
	return json.Marshal(stripped)
}

// IssueCertificate creates a signed NodeCertificate using the CA's identity.
// The issuer CA must have a private key (loaded via LoadOrCreateIdentity).
func (ca *NodeIdentity) IssueCertificate(targetNodeID string, targetPubKey []byte, mode string, ttl time.Duration) (*NodeCertificate, error) {
	if len(targetPubKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid target public key length: %d", len(targetPubKey))
	}
	if mode != "full" && mode != "transit" {
		return nil, fmt.Errorf("invalid mode: %q (must be 'full' or 'transit')", mode)
	}
	if ca.PrivateKey == nil {
		return nil, fmt.Errorf("CA identity has no private key")
	}

	now := time.Now()
	cert := &NodeCertificate{
		NodeID:    targetNodeID,
		PublicKey: targetPubKey,
		IssuedBy:  ca.NodeID,
		IssuedAt:  now.UnixMilli(),
		ExpiresAt: now.Add(ttl).UnixMilli(),
		Mode:      mode,
	}

	msg, err := cert.serializedForSigning()
	if err != nil {
		return nil, fmt.Errorf("serialize for signing: %w", err)
	}

	cert.Signature = ed25519.Sign(ca.PrivateKey, msg)
	return cert, nil
}

// VerifyCertificate checks the CA's ed25519 signature on the certificate.
func VerifyCertificate(cert *NodeCertificate, caPubKey ed25519.PublicKey) (bool, error) {
	if cert == nil {
		return false, fmt.Errorf("nil certificate")
	}
	if cert.ExpiresAt > 0 && time.Now().UnixMilli() > cert.ExpiresAt {
		return false, fmt.Errorf("certificate expired at %d", cert.ExpiresAt)
	}
	if len(cert.PublicKey) != ed25519.PublicKeySize {
		return false, fmt.Errorf("invalid public key length: %d", len(cert.PublicKey))
	}

	msg, err := cert.serializedForSigning()
	if err != nil {
		return false, fmt.Errorf("serialize for verify: %w", err)
	}

	return ed25519.Verify(caPubKey, msg, cert.Signature), nil
}

// TrustedPeerFromCertificate creates a TrustedPeer from a verified certificate.
func TrustedPeerFromCertificate(cert *NodeCertificate) *TrustedPeer {
	if cert == nil {
		return nil
	}
	return &TrustedPeer{
		NodeID:       cert.NodeID,
		Name:         cert.NodeID,
		PublicKey:    cert.PublicKey,
		Fingerprint:  fmt.Sprintf("%x", cert.PublicKey),
		AutoReconnect: true,
		Status:       TrustStatusOffline,
		LastSeen:     time.Now().UnixMilli(),
		Policy:       TrustPolicy{Mode: cert.Mode},
	}
}
