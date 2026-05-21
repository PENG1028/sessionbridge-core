package permission

import (
	"testing"

	"github.com/user/sessionnode/go-core/pkg/types"
)

func TestMapRegistry_HasCapability(t *testing.T) {
	r := NewMapRegistry(map[types.PluginID][]string{
		"shell": {"session.create", "stream.write"},
	})

	if !r.HasCapability("shell", "session.create") {
		t.Error("expected session.create for shell")
	}
	if !r.HasCapability("shell", "stream.write") {
		t.Error("expected stream.write for shell")
	}
	if r.HasCapability("shell", "fs.read") {
		t.Error("fs.read should not be declared for shell")
	}
}

func TestMapRegistry_UnknownPlugin(t *testing.T) {
	r := NewMapRegistry(nil)
	if r.HasCapability("nonexistent", "anything") {
		t.Error("expected false for unknown plugin")
	}
}

func TestMapRegistry_EmptyCaps(t *testing.T) {
	r := NewMapRegistry(map[types.PluginID][]string{
		"empty": {},
	})
	if r.HasCapability("empty", "anything") {
		t.Error("expected false for plugin with no caps")
	}
}

func TestAllPluginsCaps_Completeness(t *testing.T) {
	// Verify all known plugins have at least some caps
	known := []types.PluginID{"shell", "sessionnode-core", "file-explorer", "session"}
	for _, pid := range known {
		if len(AllPluginsCaps[pid]) == 0 {
			t.Errorf("plugin %s has no capabilities declared", pid)
		}
	}
}

func TestAllPluginsCaps_ClaudeCode(t *testing.T) {
	caps, ok := AllPluginsCaps["claude-code"]
	if !ok {
		t.Fatal("claude-code not found in AllPluginsCaps")
	}
	hasNetworkConnect := false
	hasNetworkDNS := false
	for _, c := range caps {
		if c == "network.connect" {
			hasNetworkConnect = true
		}
		if c == "network.dns" {
			hasNetworkDNS = true
		}
	}
	if !hasNetworkConnect {
		t.Error("claude-code should have network.connect")
	}
	if !hasNetworkDNS {
		t.Error("claude-code should have network.dns")
	}
}
