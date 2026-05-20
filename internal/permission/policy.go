package permission

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/user/sessionnode/go-core/pkg/types"
)

// MemPolicyStore is an in-memory policy store that maps plugin capabilities to grants.
// It implements PolicyStore with optional persistence (Phase 1+).
type MemPolicyStore struct {
	mu     sync.RWMutex
	grants map[policyKey]*PermissionGrant
}

type policyKey struct {
	pluginID    types.PluginID
	capability  string
}

// NewMemPolicyStore creates an empty policy store.
func NewMemPolicyStore() *MemPolicyStore {
	return &MemPolicyStore{
		grants: make(map[policyKey]*PermissionGrant),
	}
}

// NewAllowAllPolicy creates a policy store with allow-all grants for the given capabilities.
func NewAllowAllPolicy(caps map[types.PluginID][]string) *MemPolicyStore {
	ps := NewMemPolicyStore()
	for pid, list := range caps {
		for _, c := range list {
			ps.SetGrant(pid, c, &PermissionGrant{
				Mode:      "allow",
				GrantedAt: time.Now().UnixMilli(),
				GrantedBy: "system",
			})
		}
	}
	return ps
}

// GetGrant returns the grant for a plugin capability. Returns error if not found.
func (s *MemPolicyStore) GetGrant(pluginID types.PluginID, capability string) (*PermissionGrant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := policyKey{pluginID, capability}
	grant, ok := s.grants[key]
	if !ok {
		return nil, fmt.Errorf("no grant for %s.%s", pluginID, capability)
	}

	// Check expiration
	if grant.ExpiresAt != nil && time.Now().UnixMilli() > *grant.ExpiresAt {
		return nil, errors.New("grant expired")
	}

	return grant, nil
}

// SetGrant stores a grant for a plugin capability.
func (s *MemPolicyStore) SetGrant(pluginID types.PluginID, capability string, grant *PermissionGrant) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := policyKey{pluginID, capability}
	s.grants[key] = grant
}

// Revoke removes a grant.
func (s *MemPolicyStore) Revoke(pluginID types.PluginID, capability string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := policyKey{pluginID, capability}
	delete(s.grants, key)
}
