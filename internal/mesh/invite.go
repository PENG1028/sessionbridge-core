package mesh

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// Invite represents a one-time invite code for peer pairing.
type Invite struct {
	InviteID             string `json:"inviteId"`
	Code                 string `json:"code"`                  // short-lived one-time code, returned only on create
	CodeHash             string `json:"-"`                     // sha256(code), never returned
	CreatedAt            int64  `json:"createdAt"`
	ExpiresAt            int64  `json:"expiresAt"`
	TTLSeconds           int    `json:"ttlSeconds"`
	TrustDurationSeconds int64  `json:"trustDurationSeconds"`  // 0 = permanent
	LocalNodeID          string `json:"localNodeId"`
	LocalFingerprint     string `json:"localFingerprint"`
	LocalPublicKey       []byte `json:"localPublicKey"`
}

// InviteStore manages in-memory invite codes.
type InviteStore struct {
	mu      sync.RWMutex
	invites map[string]*Invite // keyed by inviteId
}

// NewInviteStore creates a new InviteStore.
func NewInviteStore() *InviteStore {
	return &InviteStore{
		invites: make(map[string]*Invite),
	}
}

// generateCode creates a 32-hex-char random code from 16 bytes of entropy.
func generateCode() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("mesh: generate invite code: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// hashCode returns the SHA-256 hash of a code string as a hex-encoded string.
func hashCode(code string) string {
	h := sha256.Sum256([]byte(code))
	return hex.EncodeToString(h[:])
}

// generateInviteID creates a short random invite ID (8 hex chars from 4 bytes).
func generateInviteID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("mesh: generate invite id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Create generates a new invite code tied to the given identity.
// ttlSeconds is clamped to [10, 600].
// trustDurationSeconds of 0 means permanent trust.
func (is *InviteStore) Create(identity *NodeIdentity, ttlSeconds int, trustDurationSeconds int64) (*Invite, error) {
	if identity == nil {
		return nil, fmt.Errorf("mesh: identity is nil")
	}

	if ttlSeconds < 10 {
		ttlSeconds = 10
	}
	if ttlSeconds > 600 {
		ttlSeconds = 600
	}

	code, err := generateCode()
	if err != nil {
		return nil, err
	}

	inviteID, err := generateInviteID()
	if err != nil {
		return nil, err
	}

	now := time.Now().Unix()

	invite := &Invite{
		InviteID:             inviteID,
		Code:                 code,
		CodeHash:             hashCode(code),
		CreatedAt:            now,
		ExpiresAt:            now + int64(ttlSeconds),
		TTLSeconds:           ttlSeconds,
		TrustDurationSeconds: trustDurationSeconds,
		LocalNodeID:          string(identity.NodeID),
		LocalFingerprint:     identity.Fingerprint,
		LocalPublicKey:       identity.PublicKey,
	}

	is.mu.Lock()
	is.invites[inviteID] = invite
	is.mu.Unlock()

	return invite, nil
}

// List returns all non-expired, non-revoked invites.
// Code and CodeHash are NOT included in the returned copies.
func (is *InviteStore) List() []*Invite {
	is.mu.RLock()
	defer is.mu.RUnlock()

	now := time.Now().Unix()
	var out []*Invite
	for _, inv := range is.invites {
		if inv.ExpiresAt <= now {
			continue
		}
		// Return a shallow copy without the code.
		cp := *inv
		cp.Code = ""
		out = append(out, &cp)
	}
	return out
}

// Revoke removes an invite by ID. Returns an error if the invite doesn't exist.
func (is *InviteStore) Revoke(inviteID string) error {
	is.mu.Lock()
	defer is.mu.Unlock()

	if _, ok := is.invites[inviteID]; !ok {
		return fmt.Errorf("mesh: invite %q not found", inviteID)
	}
	delete(is.invites, inviteID)
	return nil
}

// Validate checks a code against stored invites.
// Returns the matching invite (without the raw code) if valid.
// Expired and revoked invites are rejected.
// The invite is NOT consumed on successful validation.
func (is *InviteStore) Validate(code string) (*Invite, error) {
	if len(code) != 32 {
		return nil, fmt.Errorf("mesh: invalid code length")
	}

	codeHash := hashCode(code)

	is.mu.RLock()
	defer is.mu.RUnlock()

	now := time.Now().Unix()

	for _, inv := range is.invites {
		if inv.CodeHash == codeHash {
			if inv.ExpiresAt <= now {
				return nil, fmt.Errorf("mesh: invite expired")
			}
			// Return a copy without the raw code.
			cp := *inv
			cp.Code = ""
			return &cp, nil
		}
	}

	return nil, fmt.Errorf("mesh: invite not found or invalid")
}

// Consume validates a code and removes the invite (one-time use).
// Returns the matching invite (without the raw code) if valid.
// Expired and revoked invites are rejected.
// Unlike Validate, Consume deletes the invite so it cannot be reused.
func (is *InviteStore) Consume(code string) (*Invite, error) {
	if len(code) != 32 {
		return nil, fmt.Errorf("mesh: invalid code length")
	}

	codeHash := hashCode(code)

	is.mu.Lock()
	defer is.mu.Unlock()

	now := time.Now().Unix()

	for id, inv := range is.invites {
		if inv.CodeHash == codeHash {
			if inv.ExpiresAt <= now {
				return nil, fmt.Errorf("mesh: invite expired")
			}
			delete(is.invites, id) // one-time use, remove immediately
			cp := *inv
			cp.Code = ""
			return &cp, nil
		}
	}

	return nil, fmt.Errorf("mesh: invite not found or invalid")
}
