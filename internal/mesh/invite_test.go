package mesh

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestIdentity(t *testing.T) *NodeIdentity {
	dir, err := os.MkdirTemp("", "mesh-test-id-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	id, err := LoadOrCreateIdentity(dir, "test-node-01")
	if err != nil {
		t.Fatalf("LoadOrCreateIdentity failed: %v", err)
	}
	return id
}

func TestInviteCreateAndValidate(t *testing.T) {
	store := NewInviteStore()
	identity := newTestIdentity(t)

	invite, err := store.Create(identity, 60, 0)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if invite.Code == "" {
		t.Fatal("expected non-empty code")
	}
	if len(invite.Code) != 32 {
		t.Fatalf("expected 32-char hex code, got %d chars: %q", len(invite.Code), invite.Code)
	}
	if invite.InviteID == "" {
		t.Fatal("expected non-empty inviteId")
	}
	if invite.ExpiresAt <= invite.CreatedAt {
		t.Fatal("expected ExpiresAt > CreatedAt")
	}

	validated, err := store.Validate(invite.Code)
	if err != nil {
		t.Fatalf("Validate returned error for valid code: %v", err)
	}
	if validated.InviteID != invite.InviteID {
		t.Fatalf("expected inviteId %q, got %q", invite.InviteID, validated.InviteID)
	}
	if validated.LocalNodeID != identity.NodeID {
		t.Fatalf("expected LocalNodeID %q, got %q", identity.NodeID, validated.LocalNodeID)
	}
}

func TestInviteExpired(t *testing.T) {
	store := NewInviteStore()
	identity := newTestIdentity(t)

	invite, err := store.Create(identity, 10, 0)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	store.mu.Lock()
	for _, inv := range store.invites {
		inv.ExpiresAt = time.Now().Unix() - 1
	}
	store.mu.Unlock()

	_, err = store.Validate(invite.Code)
	if err == nil {
		t.Fatal("expected error for expired invite")
	}
}

func TestInviteWrongCode(t *testing.T) {
	store := NewInviteStore()
	identity := newTestIdentity(t)

	_, err := store.Create(identity, 60, 0)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	wrongCode := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	_, err = store.Validate(wrongCode)
	if err == nil {
		t.Fatal("expected error for wrong code")
	}

	_, err = store.Validate("short")
	if err == nil {
		t.Fatal("expected error for short code")
	}
}

func TestInviteRevoke(t *testing.T) {
	store := NewInviteStore()
	identity := newTestIdentity(t)

	invite, err := store.Create(identity, 60, 0)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err = store.Validate(invite.Code)
	if err != nil {
		t.Fatalf("Validate should work before revoke: %v", err)
	}

	err = store.Revoke(invite.InviteID)
	if err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}

	_, err = store.Validate(invite.Code)
	if err == nil {
		t.Fatal("expected error for revoked invite")
	}

	err = store.Revoke("nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent invite")
	}
}

func TestInviteListWithoutCodes(t *testing.T) {
	store := NewInviteStore()
	identity := newTestIdentity(t)

	invite, err := store.Create(identity, 60, 0)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	list := store.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 invite in list, got %d", len(list))
	}

	if list[0].Code != "" {
		t.Fatal("listed invite must not contain the code")
	}
	if list[0].InviteID != invite.InviteID {
		t.Fatalf("expected inviteId %q, got %q", invite.InviteID, list[0].InviteID)
	}
}

func TestInviteAcceptAddsPeerToTrustStore(t *testing.T) {
	store := NewInviteStore()
	identity := newTestIdentity(t)
	trustStore := NewTrustStore(tempDir(t))

	invite, err := store.Create(identity, 60, 3600)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	validated, err := store.Validate(invite.Code)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}

	peer := &TrustedPeer{
		NodeID:         validated.LocalNodeID,
		Name:           "test-peer",
		PublicKey:      validated.LocalPublicKey,
		Fingerprint:    validated.LocalFingerprint,
		Addresses:      []string{"ws://localhost:8080"},
		TrustExpiresAt: time.Now().Unix() + 3600,
		Status:         "pending",
		Policy:         TrustPolicy{Mode: "full"},
	}

	err = trustStore.Add(peer)
	if err != nil {
		t.Fatalf("Add peer failed: %v", err)
	}

	got, err := trustStore.Get(peer.NodeID)
	if err != nil {
		t.Fatalf("peer not found in trust store: %v", err)
	}
	if got.Status != "pending" {
		t.Fatalf("expected status 'pending', got %q", got.Status)
	}
	if got.NodeID != peer.NodeID {
		t.Fatalf("expected NodeID %q, got %q", peer.NodeID, got.NodeID)
	}
}

func TestInviteAcceptWithInvalidCode(t *testing.T) {
	store := NewInviteStore()
	identity := newTestIdentity(t)

	_, err := store.Create(identity, 60, 0)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err = store.Validate("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if err == nil {
		t.Fatal("expected error for invalid code")
	}
}

func TestNodeIdentityGetDoesNotReturnPrivateKey(t *testing.T) {
	identity := newTestIdentity(t)

	// Private key must exist on original identity
	if identity.PrivateKey == nil {
		t.Fatal("private key should be non-nil for a new identity")
	}
	if identity.NodeID == "" {
		t.Fatal("nodeId should be set")
	}
	if identity.Fingerprint == "" {
		t.Fatal("fingerprint should be set")
	}
	if identity.PublicKey == nil {
		t.Fatal("public key should be set")
	}

	// PublicIdentity() returns a struct without a PrivateKey field — that is the guarantee.
	publicID := identity.PublicIdentity()
	if publicID.NodeID != identity.NodeID {
		t.Fatalf("NodeID mismatch: %q vs %q", publicID.NodeID, identity.NodeID)
	}
	if string(publicID.PublicKey) != string(identity.PublicKey) {
		t.Fatal("PublicKey mismatch in PublicIdentity")
	}
	if publicID.Fingerprint != identity.Fingerprint {
		t.Fatal("Fingerprint mismatch in PublicIdentity")
	}
}

func TestInviteTTLClamping(t *testing.T) {
	store := NewInviteStore()
	identity := newTestIdentity(t)

	invite, err := store.Create(identity, 1, 0)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if invite.TTLSeconds != 10 {
		t.Fatalf("expected TTL clamped to 10, got %d", invite.TTLSeconds)
	}

	invite, err = store.Create(identity, 9999, 0)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if invite.TTLSeconds != 600 {
		t.Fatalf("expected TTL clamped to 600, got %d", invite.TTLSeconds)
	}
}

func TestInviteWithTrustDuration(t *testing.T) {
	store := NewInviteStore()
	identity := newTestIdentity(t)

	trustDur := int64(86400)
	invite, err := store.Create(identity, 60, trustDur)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if invite.TrustDurationSeconds != trustDur {
		t.Fatalf("expected TrustDurationSeconds %d, got %d", trustDur, invite.TrustDurationSeconds)
	}
}

func TestNewTrustStorePersistence(t *testing.T) {
	dir := tempDir(t)
	ts := NewTrustStore(dir)

	if ts == nil {
		t.Fatal("trust store should not be nil")
	}

	pubKey := []byte("somekey")
	peer := &TrustedPeer{
		NodeID:         "peer-01",
		Name:           "peer one",
		PublicKey:      pubKey,
		Fingerprint:    "abc",
		Addresses:      []string{"ws://peer1:8080"},
		TrustExpiresAt: 0,
		Status:         "trusted",
		Policy:         TrustPolicy{Mode: "full"},
	}

	err := ts.Add(peer)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	ts2 := NewTrustStore(dir)
	err = ts2.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	list := ts2.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 peer loaded from disk, got %d", len(list))
	}
	if list[0].NodeID != "peer-01" {
		t.Fatalf("expected NodeID 'peer-01', got %q", list[0].NodeID)
	}
}

func TestTrustStoreRemove(t *testing.T) {
	dir := tempDir(t)
	ts := NewTrustStore(dir)

	pubKey := []byte("key1")
	peer := &TrustedPeer{NodeID: "peer-01", PublicKey: pubKey, Status: "trusted", Policy: TrustPolicy{Mode: "full"}}
	ts.Add(peer)

	err := ts.Remove("peer-01")
	if err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	_, err = ts.Get("peer-01")
	if err == nil {
		t.Fatal("peer should have been removed")
	}

	if len(ts.List()) != 0 {
		t.Fatal("list should be empty after remove")
	}
}

func TestTrustStoreTrusted(t *testing.T) {
	dir := tempDir(t)
	ts := NewTrustStore(dir)

	pubKey1 := []byte("pubkey1")
	pubKey2 := []byte("pubkey2")
	ts.Add(&TrustedPeer{NodeID: "p1", PublicKey: pubKey1, Status: "trusted", Policy: TrustPolicy{Mode: "full"}})
	ts.Add(&TrustedPeer{NodeID: "p2", PublicKey: pubKey2, Status: "pending", Policy: TrustPolicy{Mode: "full"}})

	trusted, err := ts.Trusted("p1", pubKey1)
	if err != nil {
		t.Fatalf("Trusted returned error for p1: %v", err)
	}
	if !trusted {
		t.Fatal("p1 should be trusted")
	}

	// p2 has status "pending" — Trusted returns true when keys match
	// (only "revoked" status is rejected by Trusted).
	trusted, err = ts.Trusted("p2", pubKey2)
	if err != nil {
		t.Fatalf("Trusted returned error for p2: %v", err)
	}
	if !trusted {
		t.Fatal("p2 should be trusted (status 'pending' is not rejected, only 'revoked')")
	}

	trusted, err = ts.Trusted("nope", []byte("whatever"))
	if err != nil {
		t.Fatalf("Trusted returned unexpected error for unknown peer: %v", err)
	}
	if trusted {
		t.Fatal("unknown peer should not be trusted")
	}
}

func tempDir(t *testing.T) string {
	return filepath.Join(t.TempDir(), "trusted_peers.json")
}
