package permission

import (
	"testing"
	"time"

	"github.com/user/sessionnode/go-core/pkg/types"
)

func TestMemPolicyStore_GetGrant(t *testing.T) {
	ps := NewMemPolicyStore()

	grant := &PermissionGrant{
		Mode:      "allow",
		GrantedAt: time.Now().UnixMilli(),
		GrantedBy: "admin",
	}
	ps.SetGrant("shell", "session.create", grant)

	got, err := ps.GetGrant("shell", "session.create")
	if err != nil {
		t.Fatalf("GetGrant: %v", err)
	}
	if got.Mode != "allow" {
		t.Errorf("Mode = %q, want %q", got.Mode, "allow")
	}
}

func TestMemPolicyStore_GetGrant_NotFound(t *testing.T) {
	ps := NewMemPolicyStore()
	_, err := ps.GetGrant("shell", "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing grant")
	}
}

func TestMemPolicyStore_GetGrant_Expired(t *testing.T) {
	ps := NewMemPolicyStore()
	past := time.Now().Add(-1 * time.Hour).UnixMilli()
	ps.SetGrant("shell", "old", &PermissionGrant{
		Mode:      "allow",
		ExpiresAt: &past,
		GrantedAt: past,
		GrantedBy: "admin",
	})

	_, err := ps.GetGrant("shell", "old")
	if err == nil {
		t.Fatal("expected error for expired grant")
	}
}

func TestMemPolicyStore_Revoke(t *testing.T) {
	ps := NewMemPolicyStore()
	ps.SetGrant("shell", "session.create", &PermissionGrant{Mode: "allow"})
	ps.Revoke("shell", "session.create")

	_, err := ps.GetGrant("shell", "session.create")
	if err == nil {
		t.Error("expected error after revoke")
	}
}

func TestNewAllowAllPolicy(t *testing.T) {
	caps := map[types.PluginID][]string{
		"shell": {"session.create", "stream.write"},
	}
	ps := NewAllowAllPolicy(caps)

	grant, err := ps.GetGrant("shell", "session.create")
	if err != nil {
		t.Fatalf("GetGrant: %v", err)
	}
	if grant.Mode != "allow" {
		t.Errorf("Mode = %q", grant.Mode)
	}
	if grant.GrantedBy != "system" {
		t.Errorf("GrantedBy = %q", grant.GrantedBy)
	}

	// Unknown capability should not exist
	_, err = ps.GetGrant("shell", "nonexistent")
	if err == nil {
		t.Error("expected error for undeclared cap")
	}
}
