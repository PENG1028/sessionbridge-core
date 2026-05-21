package mesh

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

func genTestKey(t *testing.T) []byte {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return []byte(pub)
}

func TestTrustStore_NewIsEmpty(t *testing.T) {
	ts := NewTrustStore(filepath.Join(t.TempDir(), "peers.json"))
	list := ts.List()
	if len(list) != 0 {
		t.Fatalf("expected empty store, got %d peers", len(list))
	}
}

func TestTrustStore_AddAndList(t *testing.T) {
	dir := t.TempDir()
	ts := NewTrustStore(filepath.Join(dir, "peers.json"))

	pk := genTestKey(t)
	peer := &TrustedPeer{
		NodeID:      "node-1",
		Name:        "Test Node",
		PublicKey:   pk,
		Fingerprint: "abc123",
		Addresses:   []string{"ws://localhost:9090/peer/ws"},
		Policy:      TrustPolicy{Mode: "full"},
	}

	if err := ts.Add(peer); err != nil {
		t.Fatalf("Add: %v", err)
	}

	list := ts.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(list))
	}
	if list[0].NodeID != "node-1" {
		t.Fatalf("expected node-1, got %q", list[0].NodeID)
	}
}

func TestTrustStore_AddDuplicateUpdates(t *testing.T) {
	dir := t.TempDir()
	ts := NewTrustStore(filepath.Join(dir, "peers.json"))

	pk := genTestKey(t)
	peer1 := &TrustedPeer{
		NodeID:    "node-1",
		Name:      "First",
		PublicKey: pk,
		Policy:    TrustPolicy{Mode: "full"},
	}
	if err := ts.Add(peer1); err != nil {
		t.Fatalf("Add 1: %v", err)
	}

	peer2 := &TrustedPeer{
		NodeID:    "node-1",
		Name:      "Second",
		PublicKey: pk,
		Policy:    TrustPolicy{Mode: "full"},
	}
	if err := ts.Add(peer2); err != nil {
		t.Fatalf("Add 2: %v", err)
	}

	list := ts.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 peer after duplicate add, got %d", len(list))
	}
	if list[0].Name != "Second" {
		t.Fatalf("expected updated name 'Second', got %q", list[0].Name)
	}
}

func TestTrustStore_RemoveAndGet(t *testing.T) {
	dir := t.TempDir()
	ts := NewTrustStore(filepath.Join(dir, "peers.json"))

	pk := genTestKey(t)
	peer := &TrustedPeer{
		NodeID:    "node-1",
		PublicKey: pk,
		Policy:    TrustPolicy{Mode: "full"},
	}
	if err := ts.Add(peer); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := ts.Remove("node-1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	_, err := ts.Get("node-1")
	if err == nil {
		t.Fatal("expected error getting removed peer")
	}
}

func TestTrustStore_SaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")

	pk := genTestKey(t)
	ts1 := NewTrustStore(path)
	peer := &TrustedPeer{
		NodeID:         "node-1",
		Name:           "RoundTrip",
		PublicKey:      pk,
		Fingerprint:    "fp123",
		Addresses:      []string{"ws://example.com/ws"},
		TrustExpiresAt: 0,
		AutoReconnect:  true,
		Status:         "connected",
		LastSeen:       123456789,
		Policy:         TrustPolicy{Mode: "full"},
	}
	if err := ts1.Add(peer); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Load into a new store instance.
	ts2 := NewTrustStore(path)
	if err := ts2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	loaded, err := ts2.Get("node-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if loaded.NodeID != "node-1" {
		t.Fatalf("nodeID mismatch: %q", loaded.NodeID)
	}
	if loaded.Name != "RoundTrip" {
		t.Fatalf("name mismatch: %q", loaded.Name)
	}
	if loaded.Fingerprint != "fp123" {
		t.Fatalf("fingerprint mismatch: %q", loaded.Fingerprint)
	}
	if len(loaded.Addresses) != 1 || loaded.Addresses[0] != "ws://example.com/ws" {
		t.Fatalf("addresses mismatch: %v", loaded.Addresses)
	}
	if loaded.AutoReconnect != true {
		t.Fatal("AutoReconnect mismatch")
	}
	if loaded.Status != "connected" {
		t.Fatalf("status mismatch: %q", loaded.Status)
	}
	if loaded.LastSeen != 123456789 {
		t.Fatalf("LastSeen mismatch: %d", loaded.LastSeen)
	}
}

func TestTrustStore_Trusted_Matches(t *testing.T) {
	dir := t.TempDir()
	ts := NewTrustStore(filepath.Join(dir, "peers.json"))

	pk := genTestKey(t)
	peer := &TrustedPeer{
		NodeID:    "node-1",
		PublicKey: pk,
		Policy:    TrustPolicy{Mode: "full"},
	}
	if err := ts.Add(peer); err != nil {
		t.Fatalf("Add: %v", err)
	}

	ok, err := ts.Trusted("node-1", pk)
	if err != nil {
		t.Fatalf("Trusted: %v", err)
	}
	if !ok {
		t.Fatal("expected trusted=true for matching nodeID + publicKey")
	}
}

func TestTrustStore_Trusted_UnknownNode(t *testing.T) {
	dir := t.TempDir()
	ts := NewTrustStore(filepath.Join(dir, "peers.json"))

	ok, err := ts.Trusted("unknown-node", genTestKey(t))
	if err != nil {
		t.Fatalf("Trusted: %v", err)
	}
	if ok {
		t.Fatal("expected trusted=false for unknown nodeID")
	}
}

func TestTrustStore_Trusted_KeyMismatch(t *testing.T) {
	dir := t.TempDir()
	ts := NewTrustStore(filepath.Join(dir, "peers.json"))

	pk1 := genTestKey(t)
	peer := &TrustedPeer{
		NodeID:    "node-1",
		PublicKey: pk1,
		Policy:    TrustPolicy{Mode: "full"},
	}
	if err := ts.Add(peer); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Use a different key.
	pk2 := genTestKey(t)
	ok, err := ts.Trusted("node-1", pk2)
	if err == nil {
		t.Fatal("expected error for public key mismatch")
	}
	if ok {
		t.Fatal("expected trusted=false for key mismatch")
	}
}

func TestTrustStore_Trusted_Revoked(t *testing.T) {
	dir := t.TempDir()
	ts := NewTrustStore(filepath.Join(dir, "peers.json"))

	pk := genTestKey(t)
	peer := &TrustedPeer{
		NodeID:    "node-1",
		PublicKey: pk,
		Status:    "revoked",
		Policy:    TrustPolicy{Mode: "full"},
	}
	if err := ts.Add(peer); err != nil {
		t.Fatalf("Add: %v", err)
	}

	ok, err := ts.Trusted("node-1", pk)
	if err != nil {
		t.Fatalf("Trusted: %v", err)
	}
	if ok {
		t.Fatal("expected trusted=false for revoked peer")
	}
}

func TestTrustStore_LoadNonExistentFile(t *testing.T) {
	dir := t.TempDir()
	ts := NewTrustStore(filepath.Join(dir, "nonexistent.json"))

	if err := ts.Load(); err != nil {
		t.Fatalf("Load should succeed for non-existent file: %v", err)
	}
	if len(ts.List()) != 0 {
		t.Fatal("expected empty store for non-existent file")
	}
}

func TestTrustStore_LoadInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0644); err != nil {
		t.Fatalf("write bad file: %v", err)
	}

	ts := NewTrustStore(path)
	err := ts.Load()
	if err == nil {
		t.Fatal("expected error loading invalid JSON")
	}
}
