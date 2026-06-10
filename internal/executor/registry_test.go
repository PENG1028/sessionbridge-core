package executor

import (
	"testing"

	"github.com/PENG1028/sessionbridge-core/internal/capability"
)

func TestRegisteredCapabilitiesInKnownList(t *testing.T) {
	r := New(testDeps(t))
	for cap := range r.handlers {
		if !capability.KnownCapabilities[cap] {
			t.Errorf("registered capability %q not found in capability.KnownCapabilities", cap)
		}
	}
}
