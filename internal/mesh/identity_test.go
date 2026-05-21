package mesh

import (
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateIdentity_FirstTimeCreates(t *testing.T) {
	dir := t.TempDir()

	ni, err := LoadOrCreateIdentity(dir, "node_local")
	if err != nil {
		t.Fatalf("LoadOrCreateIdentity: %v", err)
	}
	if ni == nil {
		t.Fatal("expected non-nil identity")
	}
	if ni.NodeID == "" || ni.NodeID == "node_local" {
		t.Fatalf("expected generated node ID, got %q", ni.NodeID)
	}
	if len(ni.PublicKey) != 32 {
		t.Fatalf("expected 32-byte ed25519 public key, got %d", len(ni.PublicKey))
	}
	if len(ni.PrivateKey) != 64 {
		t.Fatalf("expected 64-byte ed25519 private key, got %d", len(ni.PrivateKey))
	}
	if ni.Fingerprint == "" {
		t.Fatal("expected non-empty fingerprint")
	}
	if ni.CreatedAt == 0 {
		t.Fatal("expected non-zero CreatedAt")
	}

	// Verify the file was written.
	path := identityFile(dir)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("identity.json was not created")
	}
}

func TestLoadOrCreateIdentity_SecondTimeSame(t *testing.T) {
	dir := t.TempDir()

	ni1, err := LoadOrCreateIdentity(dir, "node_local")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	ni2, err := LoadOrCreateIdentity(dir, "node_local")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if ni1.NodeID != ni2.NodeID {
		t.Fatalf("node ID changed: %q vs %q", ni1.NodeID, ni2.NodeID)
	}
	if ni1.Fingerprint != ni2.Fingerprint {
		t.Fatalf("fingerprint changed: %q vs %q", ni1.Fingerprint, ni2.Fingerprint)
	}
	if ni1.CreatedAt != ni2.CreatedAt {
		t.Fatalf("CreatedAt changed: %d vs %d", ni1.CreatedAt, ni2.CreatedAt)
	}
}

func TestLoadOrCreateIdentity_ExplicitNodeID(t *testing.T) {
	dir := t.TempDir()

	ni, err := LoadOrCreateIdentity(dir, "my-custom-node")
	if err != nil {
		t.Fatalf("LoadOrCreateIdentity: %v", err)
	}
	if ni.NodeID != "my-custom-node" {
		t.Fatalf("expected nodeID 'my-custom-node', got %q", ni.NodeID)
	}
}

func TestSignVerify_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	ni, err := LoadOrCreateIdentity(dir, "node_local")
	if err != nil {
		t.Fatalf("LoadOrCreateIdentity: %v", err)
	}
	// The identity loaded from disk has no private key, so we must work
	// with the in-memory copy that still holds it.
	message := []byte("hello, mesh")

	sig := ni.Sign(message)
	if len(sig) != ed25519.SignatureSize {
		t.Fatalf("expected signature size %d, got %d", ed25519.SignatureSize, len(sig))
	}

	if !ni.Verify(ni.PublicKey, message, sig) {
		t.Fatal("Verify returned false for valid signature")
	}
}

func TestSignVerify_WrongKey(t *testing.T) {
	dir := t.TempDir()

	ni, err := LoadOrCreateIdentity(dir, "node_local")
	if err != nil {
		t.Fatalf("LoadOrCreateIdentity: %v", err)
	}

	message := []byte("hello, mesh")
	sig := ni.Sign(message)

	// Generate a different key to verify against.
	otherPub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate other key: %v", err)
	}

	if ni.Verify(otherPub, message, sig) {
		t.Fatal("Verify returned true with wrong public key")
	}
}

func TestSignVerify_WrongMessage(t *testing.T) {
	dir := t.TempDir()

	ni, err := LoadOrCreateIdentity(dir, "node_local")
	if err != nil {
		t.Fatalf("LoadOrCreateIdentity: %v", err)
	}

	sig := ni.Sign([]byte("original message"))

	if ni.Verify(ni.PublicKey, []byte("tampered message"), sig) {
		t.Fatal("Verify returned true for tampered message")
	}
}

func TestPublicIdentity_ExcludesPrivateKey(t *testing.T) {
	dir := t.TempDir()

	ni, err := LoadOrCreateIdentity(dir, "node_local")
	if err != nil {
		t.Fatalf("LoadOrCreateIdentity: %v", err)
	}

	pi := ni.PublicIdentity()
	if pi == nil {
		t.Fatal("expected non-nil PublicIdentity")
	}
	if pi.NodeID != ni.NodeID {
		t.Fatalf("node ID mismatch: %q vs %q", pi.NodeID, ni.NodeID)
	}
	if pi.Fingerprint != ni.Fingerprint {
		t.Fatalf("fingerprint mismatch: %q vs %q", pi.Fingerprint, ni.Fingerprint)
	}
	if pi.CreatedAt != ni.CreatedAt {
		t.Fatalf("CreatedAt mismatch: %d vs %d", pi.CreatedAt, ni.CreatedAt)
	}
	if len(pi.PublicKey) != len(ni.PublicKey) {
		t.Fatal("public key length mismatch")
	}
	// PublicIdentity struct has no PrivateKey field, so it is structurally
	// excluded. We also verify the JSON representation does not contain it.
}

func TestIdentityJSON_NoPrivateKey(t *testing.T) {
	dir := t.TempDir()

	ni, err := LoadOrCreateIdentity(dir, "node_local")
	if err != nil {
		t.Fatalf("LoadOrCreateIdentity: %v", err)
	}

	raw, err := json.Marshal(ni)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, ok := m["privateKey"]; ok {
		t.Fatal("privateKey should not appear in JSON output")
	}
	if _, ok := m["PrivateKey"]; ok {
		t.Fatal("PrivateKey should not appear in JSON output")
	}

	// Verify round-trip: read back from disk.
	ni2, err := LoadOrCreateIdentity(dir, "node_local")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	// The reloaded identity should NOT have a private key (it was stripped on save).
	if len(ni2.PrivateKey) != 0 {
		t.Fatal("reloaded identity should have empty PrivateKey")
	}
}

func TestLoadOrCreateIdentity_PersistsFile(t *testing.T) {
	dir := t.TempDir()

	ni, err := LoadOrCreateIdentity(dir, "node_local")
	if err != nil {
		t.Fatalf("LoadOrCreateIdentity: %v", err)
	}

	// Read the raw file and verify structure.
	data, err := os.ReadFile(filepath.Join(dir, "identity.json"))
	if err != nil {
		t.Fatalf("read identity.json: %v", err)
	}

	var parsed NodeIdentity
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal from disk: %v", err)
	}

	if parsed.NodeID != ni.NodeID {
		t.Fatalf("persisted nodeID mismatch: %q vs %q", parsed.NodeID, ni.NodeID)
	}
	if parsed.Fingerprint != ni.Fingerprint {
		t.Fatalf("persisted fingerprint mismatch")
	}
}

