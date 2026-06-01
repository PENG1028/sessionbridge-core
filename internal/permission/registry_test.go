package permission

import (
	"testing"

	"github.com/user/sessionnode/go-core/internal/capability"
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

func TestTerminalPluginCapabilitiesInKnownList(t *testing.T) {
	terminalCaps := []string{
		"session.create", "session.list", "session.get", "session.destroy",
		"stream.write", "stream.subscribe", "stream.replay", "stream.tail",
		"node.list",
		"process.spawn", "process.signal", "process.resize",
		"run.create", "run.list", "run.info", "run.stop", "run.attach", "run.updatePolicy",
	}
	for _, cap := range terminalCaps {
		if !capability.KnownCapabilities[cap] {
			t.Errorf("terminal capability %q not in KnownCapabilities", cap)
		}
	}
}

func TestSessionNodeCoreAppUICapabilities(t *testing.T) {
	r := NewMapRegistry(AllPluginsCaps)
	caps := []string{
		"approval.list",
		"run.list",
	}
	for _, cap := range caps {
		if !r.HasCapability("sessionnode-core", cap) {
			t.Errorf("sessionnode-core should declare %q for App UI proxy calls", cap)
		}
	}
}
