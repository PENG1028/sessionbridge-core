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
	if len(ni2.PrivateKey) != 64 {
		t.Fatalf("reloaded identity should have private key, got %d bytes", len(ni2.PrivateKey))
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

func TestLoadOrCreateIdentity_SignVerifyAfterReload(t *testing.T) {
	dir := t.TempDir()

	// Create identity and sign.
	ni1, err := LoadOrCreateIdentity(dir, "node_local")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	message := []byte("hello, mesh")
	sig1, err := ni1.Sign(message)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// Reload from disk.
	ni2, err := LoadOrCreateIdentity(dir, "node_local")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	// Verify the original signature with the reloaded public key.
	if !ed25519.Verify(ed25519.PublicKey(ni2.PublicKey), message, sig1) {
		t.Fatal("reloaded public key does not verify signature from first session")
	}

	// Sign with reloaded identity and verify.
	sig2, err := ni2.Sign(message)
	if err != nil {
		t.Fatalf("sign after reload: %v", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(ni1.PublicKey), message, sig2) {
		t.Fatal("reloaded identity produced invalid signature")
	}

	// Both signatures should match (ed25519 is deterministic).
	if len(sig1) != len(sig2) {
		t.Fatal("signatures differ between sessions")
	}
}

func TestLoadOrCreateIdentity_CrossSessionSignVerify(t *testing.T) {
	dir := t.TempDir()

	// Session 1: create identity and sign.
	ni1, err := LoadOrCreateIdentity(dir, "node_local")
	if err != nil {
		t.Fatalf("session 1 create: %v", err)
	}

	message := []byte("cross-session test message")
	sig1, err := ni1.Sign(message)
	if err != nil {
		t.Fatalf("session 1 sign: %v", err)
	}

	// Simulate a new process by loading again (identity persists on disk).
	ni2, err := LoadOrCreateIdentity(dir, "node_local")
	if err != nil {
		t.Fatalf("session 2 load: %v", err)
	}

	// Session 2 verifies session 1's signature.
	if !ed25519.Verify(ed25519.PublicKey(ni2.PublicKey), message, sig1) {
		t.Fatal("session 2 cannot verify session 1's signature")
	}

	// Session 2 signs and session 1 verifies.
	sig2, err := ni2.Sign(message)
	if err != nil {
		t.Fatalf("session 2 sign: %v", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(ni1.PublicKey), message, sig2) {
		t.Fatal("session 1 cannot verify session 2's signature")
	}
}

func TestSignVerify_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	ni, err := LoadOrCreateIdentity(dir, "node_local")
	if err != nil {
		t.Fatalf("LoadOrCreateIdentity: %v", err)
	}

	message := []byte("hello, mesh")
	sig, err := ni.Sign(message)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
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
	sig, err := ni.Sign(message)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

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

	sig, err := ni.Sign([]byte("original message"))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if ni.Verify(ni.PublicKey, []byte("tampered message"), sig) {
		t.Fatal("Verify returned true for tampered message")
	}
}

func TestSign_NilPrivateKey_ReturnsError(t *testing.T) {
	ni := &NodeIdentity{
		NodeID:      "test",
		PublicKey:   make([]byte, 32),
		PrivateKey:  nil,
		Fingerprint: "abc",
	}
	_, err := ni.Sign([]byte("test"))
	if err == nil {
		t.Fatal("expected error for nil PrivateKey")
	}
}

func TestSign_EmptyPrivateKey_ReturnsError(t *testing.T) {
	ni := &NodeIdentity{
		NodeID:      "test",
		PublicKey:   make([]byte, 32),
		PrivateKey:  []byte{},
		Fingerprint: "abc",
	}
	_, err := ni.Sign([]byte("test"))
	if err == nil {
		t.Fatal("expected error for empty PrivateKey")
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
	// PublicIdentity struct has no PrivateKey field — structurally excluded.
}

func TestIdentityJSON_PrivateKeyIsPersisted(t *testing.T) {
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

	// Private key MUST appear in JSON output (needed for persistence).
	if _, ok := m["privateKey"]; !ok {
		t.Fatal("privateKey should appear in JSON output for disk persistence")
	}

	// Verify round-trip: read back from disk, private key must be present.
	ni2, err := LoadOrCreateIdentity(dir, "node_local")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(ni2.PrivateKey) != 64 {
		t.Fatalf("reloaded identity should have 64-byte private key, got %d", len(ni2.PrivateKey))
	}

	// And Sign must work.
	msg := []byte("persist-test")
	sig, err := ni2.Sign(msg)
	if err != nil {
		t.Fatalf("sign after reload: %v", err)
	}
	if !ni2.Verify(ni2.PublicKey, msg, sig) {
		t.Fatal("verify after reload failed")
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
	// Private key must be persisted.
	if len(parsed.PrivateKey) != 64 {
		t.Fatalf("persisted private key should be 64 bytes, got %d", len(parsed.PrivateKey))
	}
	if len(parsed.PublicKey) != 32 {
		t.Fatalf("persisted public key should be 32 bytes, got %d", len(parsed.PublicKey))
	}
}

func TestLoadOrCreateIdentity_Validation_ShortPublicKey(t *testing.T) {
	dir := t.TempDir()

	// Write a corrupt identity with a short public key.
	badID := `{"nodeId":"test","publicKey":"AAA=","privateKey":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA==","fingerprint":"abc","createdAt":1}`
	if err := os.WriteFile(filepath.Join(dir, "identity.json"), []byte(badID), 0600); err != nil {
		t.Fatalf("write bad identity: %v", err)
	}

	_, err := LoadOrCreateIdentity(dir, "node_local")
	if err == nil {
		t.Fatal("expected validation error for short publicKey")
	}
	t.Logf("correctly rejected: %v", err)
}

func TestLoadOrCreateIdentity_Validation_ShortPrivateKey(t *testing.T) {
	dir := t.TempDir()

	// Create valid identity first to get a proper public key.
	ni, _ := LoadOrCreateIdentity(dir, "node_local")
	os.Remove(filepath.Join(dir, "identity.json"))

	// Now write one with a short private key but valid public key.
	bad := NodeIdentity{
		NodeID:      ni.NodeID,
		PublicKey:   ni.PublicKey,
		PrivateKey:  []byte("short"),
		Fingerprint: ni.Fingerprint,
		CreatedAt:   ni.CreatedAt,
	}
	data, _ := json.Marshal(bad)
	if err := os.WriteFile(filepath.Join(dir, "identity.json"), data, 0600); err != nil {
		t.Fatalf("write bad identity: %v", err)
	}

	_, err := LoadOrCreateIdentity(dir, "node_local")
	if err == nil {
		t.Fatal("expected validation error for short privateKey")
	}
	t.Logf("correctly rejected: %v", err)
}

func TestLoadOrCreateIdentity_Validation_FingerprintMismatch(t *testing.T) {
	dir := t.TempDir()

	ni, _ := LoadOrCreateIdentity(dir, "node_local")
	os.Remove(filepath.Join(dir, "identity.json"))

	bad := NodeIdentity{
		NodeID:      ni.NodeID,
		PublicKey:   ni.PublicKey,
		PrivateKey:  ni.PrivateKey,
		Fingerprint: "deadbeefdeadbeef",
		CreatedAt:   ni.CreatedAt,
	}
	data, _ := json.Marshal(bad)
	if err := os.WriteFile(filepath.Join(dir, "identity.json"), data, 0600); err != nil {
		t.Fatalf("write bad identity: %v", err)
	}

	_, err := LoadOrCreateIdentity(dir, "node_local")
	if err == nil {
		t.Fatal("expected validation error for fingerprint mismatch")
	}
	t.Logf("correctly rejected: %v", err)
}

func TestLoadOrCreateIdentity_Validation_KeyPairMismatch(t *testing.T) {
	dir := t.TempDir()

	ni, _ := LoadOrCreateIdentity(dir, "node_local")

	// Generate a different key pair.
	wrongPub, wrongPriv, _ := ed25519.GenerateKey(nil)
	_ = wrongPriv

	os.Remove(filepath.Join(dir, "identity.json"))

	bad := NodeIdentity{
		NodeID:      ni.NodeID,
		PublicKey:   wrongPub,      // wrong public key
		PrivateKey:  ni.PrivateKey, // original private key
		Fingerprint: ni.Fingerprint,
		CreatedAt:   ni.CreatedAt,
	}
	data, _ := json.Marshal(bad)
	if err := os.WriteFile(filepath.Join(dir, "identity.json"), data, 0600); err != nil {
		t.Fatalf("write bad identity: %v", err)
	}

	_, err := LoadOrCreateIdentity(dir, "node_local")
	if err == nil {
		t.Fatal("expected validation error for key-pair mismatch")
	}
	t.Logf("correctly rejected: %v", err)
}
