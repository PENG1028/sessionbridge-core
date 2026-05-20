package permission

import (
	"encoding/json"
	"testing"

	"github.com/user/sessionnode/go-core/pkg/protocol"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// --- Mock implementations ---

type mockPluginCapRegistry struct {
	capabilities map[string]map[string]bool // pluginID -> capability -> declared
}

func (m *mockPluginCapRegistry) HasCapability(pluginID types.PluginID, capability string) bool {
	if caps, ok := m.capabilities[string(pluginID)]; ok {
		return caps[capability]
	}
	return false
}

type mockPolicyStore struct {
	grants map[string]map[string]*PermissionGrant // pluginID -> capability -> grant
	err    error
}

func (m *mockPolicyStore) GetGrant(pluginID types.PluginID, capability string) (*PermissionGrant, error) {
	if m.err != nil {
		return nil, m.err
	}
	if grants, ok := m.grants[string(pluginID)]; ok {
		if g, ok := grants[capability]; ok {
			return g, nil
		}
	}
	return nil, &PluginPermissionError{Code: protocol.ErrCodeNotGranted}
}

func makePayload(path string) json.RawMessage {
	data, _ := json.Marshal(map[string]string{"path": path})
	return data
}

func TestCheck_Success_NoConstraints(t *testing.T) {
	c := newTestChecker(map[string]map[string]bool{
		"test-plugin": {"fs.read": true},
	}, map[string]map[string]*PermissionGrant{
		"test-plugin": {"fs.read": {Mode: "allow"}},
	})

	req := &types.CapabilityRequest{
		PluginID:   "test-plugin",
		Capability: "fs.read",
	}
	if err := c.Check(req); err != nil {
		t.Errorf("expected success, got: %v", err)
	}
}

func TestCheck_Success_WithConstraints(t *testing.T) {
	c := newTestChecker(map[string]map[string]bool{
		"test-plugin": {"fs.read": true},
	}, map[string]map[string]*PermissionGrant{
		"test-plugin": {
			"fs.read": {
				Mode: "allow",
				Constraints: &types.PermissionConstraints{
					Allow: []string{"~/.claude/**"},
				},
			},
		},
	})

	req := &types.CapabilityRequest{
		PluginID:   "test-plugin",
		Capability: "fs.read",
		Payload:    makePayload("~/.claude/settings.json"),
	}
	if err := c.Check(req); err != nil {
		t.Errorf("expected success, got: %v", err)
	}
}

func TestCheck_CapabilityNotDeclared(t *testing.T) {
	c := newTestChecker(map[string]map[string]bool{
		"test-plugin": {"fs.read": true},
	}, map[string]map[string]*PermissionGrant{
		"test-plugin": {"fs.read": {Mode: "allow"}},
	})

	req := &types.CapabilityRequest{
		PluginID:   "test-plugin",
		Capability: "process.spawn", // not declared
	}
	err := c.Check(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if permErr, ok := err.(*PluginPermissionError); ok {
		if permErr.Code != protocol.ErrCodeCapNotDeclared {
			t.Errorf("expected code %s, got %s", protocol.ErrCodeCapNotDeclared, permErr.Code)
		}
	} else {
		t.Errorf("expected *PluginPermissionError, got %T: %v", err, err)
	}
}

func TestCheck_NotGranted(t *testing.T) {
	c := newTestChecker(map[string]map[string]bool{
		"test-plugin": {"fs.read": true},
	}, map[string]map[string]*PermissionGrant{})

	req := &types.CapabilityRequest{
		PluginID:   "test-plugin",
		Capability: "fs.read",
	}
	err := c.Check(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if permErr, ok := err.(*PluginPermissionError); ok {
		if permErr.Code != protocol.ErrCodeNotGranted {
			t.Errorf("expected code %s, got %s", protocol.ErrCodeNotGranted, permErr.Code)
		}
	} else {
		t.Errorf("expected *PluginPermissionError, got %T: %v", err, err)
	}
}

func TestCheck_ModeDeny(t *testing.T) {
	c := newTestChecker(map[string]map[string]bool{
		"test-plugin": {"fs.read": true},
	}, map[string]map[string]*PermissionGrant{
		"test-plugin": {"fs.read": {Mode: "deny"}},
	})

	req := &types.CapabilityRequest{
		PluginID:   "test-plugin",
		Capability: "fs.read",
	}
	err := c.Check(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if permErr, ok := err.(*PluginPermissionError); ok {
		if permErr.Code != protocol.ErrCodePermissionDenied {
			t.Errorf("expected code %s, got %s", protocol.ErrCodePermissionDenied, permErr.Code)
		}
	} else {
		t.Errorf("expected *PluginPermissionError, got %T: %v", err, err)
	}
}

func TestCheck_ModeAsk(t *testing.T) {
	c := newTestChecker(map[string]map[string]bool{
		"test-plugin": {"fs.read": true},
	}, map[string]map[string]*PermissionGrant{
		"test-plugin": {"fs.read": {Mode: "ask"}},
	})

	req := &types.CapabilityRequest{
		PluginID:   "test-plugin",
		Capability: "fs.read",
	}
	err := c.Check(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if permErr, ok := err.(*PluginPermissionError); ok {
		if permErr.Code != protocol.ErrCodeNeedApproval {
			t.Errorf("expected code %s, got %s", protocol.ErrCodeNeedApproval, permErr.Code)
		}
	} else {
		t.Errorf("expected *PluginPermissionError, got %T: %v", err, err)
	}
}

func TestCheck_ModeUnknown(t *testing.T) {
	c := newTestChecker(map[string]map[string]bool{
		"test-plugin": {"fs.read": true},
	}, map[string]map[string]*PermissionGrant{
		"test-plugin": {"fs.read": {Mode: "whatisthis"}},
	})

	req := &types.CapabilityRequest{
		PluginID:   "test-plugin",
		Capability: "fs.read",
	}
	err := c.Check(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCheck_ConstraintsMatch(t *testing.T) {
	c := newTestChecker(map[string]map[string]bool{
		"test-plugin": {"fs.read": true},
	}, map[string]map[string]*PermissionGrant{
		"test-plugin": {
			"fs.read": {
				Mode: "allow",
				Constraints: &types.PermissionConstraints{
					Allow: []string{"/home/**"},
				},
			},
		},
	})

	req := &types.CapabilityRequest{
		PluginID:   "test-plugin",
		Capability: "fs.read",
		Payload:    makePayload("/home/user/file.txt"),
	}
	if err := c.Check(req); err != nil {
		t.Errorf("expected success, got: %v", err)
	}
}

func TestCheck_ConstraintsNoMatch(t *testing.T) {
	c := newTestChecker(map[string]map[string]bool{
		"test-plugin": {"fs.read": true},
	}, map[string]map[string]*PermissionGrant{
		"test-plugin": {
			"fs.read": {
				Mode: "allow",
				Constraints: &types.PermissionConstraints{
					Allow: []string{"/work/**"},
				},
			},
		},
	})

	req := &types.CapabilityRequest{
		PluginID:   "test-plugin",
		Capability: "fs.read",
		Payload:    makePayload("/etc/passwd"),
	}
	err := c.Check(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if permErr, ok := err.(*PluginPermissionError); ok {
		if permErr.Code != protocol.ErrCodePathNotAllowed {
			t.Errorf("expected code %s, got %s", protocol.ErrCodePathNotAllowed, permErr.Code)
		}
	} else {
		t.Errorf("expected *PluginPermissionError, got %T: %v", err, err)
	}
}

func TestCheck_StoreError(t *testing.T) {
	registry := &mockPluginCapRegistry{
		capabilities: map[string]map[string]bool{
			"test-plugin": {"fs.read": true},
		},
	}
	policy := &mockPolicyStore{
		err: &PluginPermissionError{Code: protocol.ErrCodeInternalError, Message: "store unavailable"},
		grants: map[string]map[string]*PermissionGrant{
			"test-plugin": {"fs.read": {Mode: "allow"}},
		},
	}
	c := NewChecker(registry, policy)

	req := &types.CapabilityRequest{
		PluginID:   "test-plugin",
		Capability: "fs.read",
	}
	err := c.Check(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCheck_EmptyRegistry(t *testing.T) {
	c := newTestChecker(nil, map[string]map[string]*PermissionGrant{})

	req := &types.CapabilityRequest{
		PluginID:   "unknown-plugin",
		Capability: "fs.read",
	}
	err := c.Check(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if permErr, ok := err.(*PluginPermissionError); ok {
		if permErr.Code != protocol.ErrCodeCapNotDeclared {
			t.Errorf("expected code %s, got %s", protocol.ErrCodeCapNotDeclared, permErr.Code)
		}
	} else {
		t.Errorf("expected *PluginPermissionError, got %T: %v", err, err)
	}
}

func TestPluginPermissionError_ErrorInterface(t *testing.T) {
	err := &PluginPermissionError{Code: "TEST", Message: "test message"}
	if err.Error() != "TEST: test message" {
		t.Errorf("unexpected Error(): %s", err.Error())
	}
	var e error = err
	if e == nil {
		t.Error("PluginPermissionError should satisfy error interface")
	}
}

func TestCheck_TargetNodeAllowed(t *testing.T) {
	c := newTestChecker(map[string]map[string]bool{
		"test-plugin": {"fs.read": true},
	}, map[string]map[string]*PermissionGrant{
		"test-plugin": {
			"fs.read": {
				Mode: "allow",
				Constraints: &types.PermissionConstraints{
					TargetNodes: []string{"node-vps", "node-local"},
				},
			},
		},
	})

	req := &types.CapabilityRequest{
		PluginID:     "test-plugin",
		Capability:   "fs.read",
		TargetNodeID: "node-vps",
	}
	if err := c.Check(req); err != nil {
		t.Errorf("expected success for node-vps, got: %v", err)
	}
}

func TestCheck_TargetNodeDenied(t *testing.T) {
	c := newTestChecker(map[string]map[string]bool{
		"test-plugin": {"fs.read": true},
	}, map[string]map[string]*PermissionGrant{
		"test-plugin": {
			"fs.read": {
				Mode: "allow",
				Constraints: &types.PermissionConstraints{
					TargetNodes: []string{"node-vps"},
				},
			},
		},
	})

	req := &types.CapabilityRequest{
		PluginID:     "test-plugin",
		Capability:   "fs.read",
		TargetNodeID: "node-other",
	}
	err := c.Check(req)
	if err == nil {
		t.Fatal("expected error for disallowed target node, got nil")
	}
	if permErr, ok := err.(*PluginPermissionError); ok {
		if permErr.Code != protocol.ErrCodeNodeNotAllowed {
			t.Errorf("expected code %s, got %s", protocol.ErrCodeNodeNotAllowed, permErr.Code)
		}
	} else {
		t.Errorf("expected *PluginPermissionError, got %T: %v", err, err)
	}
}

func TestCheck_TargetNodeLocalRejectedWhenScopeSet(t *testing.T) {
	c := newTestChecker(map[string]map[string]bool{
		"test-plugin": {"fs.read": true},
	}, map[string]map[string]*PermissionGrant{
		"test-plugin": {
			"fs.read": {
				Mode: "allow",
				Constraints: &types.PermissionConstraints{
					TargetNodes: []string{"node-vps"},
				},
			},
		},
	})

	// Empty TargetNodeID = local execution, but scope explicitly lists nodes.
	req := &types.CapabilityRequest{
		PluginID:   "test-plugin",
		Capability: "fs.read",
		// TargetNodeID empty = local
	}
	err := c.Check(req)
	if err == nil {
		t.Fatal("expected error for local execution with node-scoped grant, got nil")
	}
	if permErr, ok := err.(*PluginPermissionError); ok {
		if permErr.Code != protocol.ErrCodeNodeNotAllowed {
			t.Errorf("expected code %s, got %s", protocol.ErrCodeNodeNotAllowed, permErr.Code)
		}
	} else {
		t.Errorf("expected *PluginPermissionError, got %T: %v", err, err)
	}
}

func TestCheck_TargetNodeAnyWhenScopeEmpty(t *testing.T) {
	c := newTestChecker(map[string]map[string]bool{
		"test-plugin": {"fs.read": true},
	}, map[string]map[string]*PermissionGrant{
		"test-plugin": {
			"fs.read": {
				Mode: "allow",
				Constraints: &types.PermissionConstraints{
					// No TargetNodes = all nodes allowed
				},
			},
		},
	})

	// Remote execution with any target should work.
	req := &types.CapabilityRequest{
		PluginID:     "test-plugin",
		Capability:   "fs.read",
		TargetNodeID: "any-node",
	}
	if err := c.Check(req); err != nil {
		t.Errorf("expected success for unrestricted target, got: %v", err)
	}

	// Local execution should also work.
	req2 := &types.CapabilityRequest{
		PluginID:   "test-plugin",
		Capability: "fs.read",
	}
	if err := c.Check(req2); err != nil {
		t.Errorf("expected success for local execution without scope, got: %v", err)
	}
}

// --- Helpers ---

func newTestChecker(caps map[string]map[string]bool, grants map[string]map[string]*PermissionGrant) *Checker {
	if caps == nil {
		caps = make(map[string]map[string]bool)
	}
	if grants == nil {
		grants = make(map[string]map[string]*PermissionGrant)
	}
	return NewChecker(
		&mockPluginCapRegistry{capabilities: caps},
		&mockPolicyStore{grants: grants},
	)
}
