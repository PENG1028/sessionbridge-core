package mesh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"
)

// TestIssueAndVerifyCertificate tests a full issue/verify cycle.
func TestIssueAndVerifyCertificate(t *testing.T) {
	// Create a CA identity (simulates a hub's identity)
	caPub, caPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	ca := &NodeIdentity{
		NodeID:     "ca-hub",
		PublicKey:  caPub,
		PrivateKey: caPriv,
	}

	// Create a target node key
	targetPub, _, _ := ed25519.GenerateKey(rand.Reader)

	// Issue certificate
	cert, err := ca.IssueCertificate("node-client", targetPub, "transit", 24*time.Hour)
	if err != nil {
		t.Fatalf("IssueCertificate: %v", err)
	}

	// Verify certificate
	valid, err := VerifyCertificate(cert, caPub)
	if err != nil {
		t.Fatalf("VerifyCertificate: %v", err)
	}
	if !valid {
		t.Fatal("certificate should be valid")
	}

	t.Logf("cert: node=%s issuer=%s mode=%s", cert.NodeID, cert.IssuedBy, cert.Mode)
}

// TestVerifyCertificate_WrongKey verifies that wrong CA key fails.
func TestVerifyCertificate_WrongKey(t *testing.T) {
	caPub, caPriv, _ := ed25519.GenerateKey(rand.Reader)
	ca := &NodeIdentity{NodeID: "ca", PublicKey: caPub, PrivateKey: caPriv}
	targetPub, _, _ := ed25519.GenerateKey(rand.Reader)

	cert, _ := ca.IssueCertificate("node", targetPub, "full", time.Hour)

	// Wrong CA public key
	wrongPub, _, _ := ed25519.GenerateKey(rand.Reader)
	valid, err := VerifyCertificate(cert, wrongPub)
	if err != nil {
		t.Fatalf("VerifyCertificate: %v", err)
	}
	if valid {
		t.Fatal("should be invalid with wrong CA key")
	}
}

// TestVerifyCertificate_Expired verifies expired certificates are rejected.
func TestVerifyCertificate_Expired(t *testing.T) {
	caPub, caPriv, _ := ed25519.GenerateKey(rand.Reader)
	ca := &NodeIdentity{NodeID: "ca", PublicKey: caPub, PrivateKey: caPriv}
	targetPub, _, _ := ed25519.GenerateKey(rand.Reader)

	// Issue with -1 hour TTL (already expired)
	cert, err := ca.IssueCertificate("node", targetPub, "full", -1*time.Hour)
	if err != nil {
		t.Fatalf("IssueCertificate: %v", err)
	}

	_, err = VerifyCertificate(cert, caPub)
	if err == nil {
		t.Fatal("expired certificate should return error")
	}
	t.Logf("expired cert correctly rejected: %v", err)
}

// TestIssueCertificate_InvalidKey verifies validation of bad input.
func TestIssueCertificate_InvalidKey(t *testing.T) {
	caPub, caPriv, _ := ed25519.GenerateKey(rand.Reader)
	ca := &NodeIdentity{NodeID: "ca", PublicKey: caPub, PrivateKey: caPriv}

	// Wrong public key length
	_, err := ca.IssueCertificate("node", []byte{1, 2, 3}, "full", time.Hour)
	if err == nil {
		t.Fatal("should reject invalid public key length")
	}
	t.Logf("invalid key correctly rejected: %v", err)

	// Invalid mode
	targetPub, _, _ := ed25519.GenerateKey(rand.Reader)
	_, err = ca.IssueCertificate("node", targetPub, "invalid_mode", time.Hour)
	if err == nil {
		t.Fatal("should reject invalid mode")
	}
}

// TestTrustedPeerFromCertificate verifies certificate-to-peer conversion.
func TestTrustedPeerFromCertificate(t *testing.T) {
	caPub, caPriv, _ := ed25519.GenerateKey(rand.Reader)
	ca := &NodeIdentity{NodeID: "ca", PublicKey: caPub, PrivateKey: caPriv}
	targetPub, _, _ := ed25519.GenerateKey(rand.Reader)

	cert, _ := ca.IssueCertificate("client-node", targetPub, "transit", 7*24*time.Hour)

	peer := TrustedPeerFromCertificate(cert)
	if peer == nil {
		t.Fatal("TrustedPeerFromCertificate returned nil")
	}
	if peer.NodeID != "client-node" {
		t.Errorf("NodeID = %q, want 'client-node'", peer.NodeID)
	}
	if peer.Policy.Mode != "transit" {
		t.Errorf("Mode = %q, want 'transit'", peer.Policy.Mode)
	}
	if peer.Status != TrustStatusOffline {
		t.Errorf("Status = %q, want 'offline'", peer.Status)
	}
	t.Logf("peer: id=%s mode=%s", peer.NodeID, peer.Policy.Mode)
}

// TestCertificate_SerializationRoundTrip verifies JSON round-trip.
func TestCertificate_SerializationRoundTrip(t *testing.T) {
	caPub, caPriv, _ := ed25519.GenerateKey(rand.Reader)
	ca := &NodeIdentity{NodeID: "ca", PublicKey: caPub, PrivateKey: caPriv}
	targetPub, _, _ := ed25519.GenerateKey(rand.Reader)

	orig, _ := ca.IssueCertificate("node-x", targetPub, "full", time.Hour)

	// Marshal and unmarshal
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored NodeCertificate
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if restored.NodeID != orig.NodeID {
		t.Errorf("NodeID = %q, want %q", restored.NodeID, orig.NodeID)
	}
	if restored.Mode != orig.Mode {
		t.Errorf("Mode = %q, want %q", restored.Mode, orig.Mode)
	}

	// Verify still valid after round-trip
	valid, err := VerifyCertificate(&restored, caPub)
	if err != nil {
		t.Fatalf("verify after round-trip: %v", err)
	}
	if !valid {
		t.Fatal("certificate should be valid after round-trip")
	}
	t.Log("JSON round-trip: OK")
}
