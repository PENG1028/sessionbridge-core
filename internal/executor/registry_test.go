package executor

import (
	"testing"

	"github.com/user/sessionnode/go-core/internal/pluginmanifest"
)

func TestRegisteredCapabilitiesInKnownList(t *testing.T) {
	r := New(testDeps(t))
	for cap := range r.handlers {
		if !pluginmanifest.KnownCapabilities[cap] {
			t.Errorf("registered capability %q not found in pluginmanifest.KnownCapabilities", cap)
		}
	}
}
