package main

import (
	"os"
	"testing"
)

func TestValidatePublicAccess_TokenSet(t *testing.T) {
	// Token is set → no failure regardless of address
	validatePublicAccess("0.0.0.0:8080", "mytoken")
	// Should not call log.Fatalf
}

func TestValidatePublicAccess_AllowInsecure(t *testing.T) {
	// ALLOW_INSECURE=1 → no failure on public address
	os.Setenv("SESSIONNODE_ALLOW_INSECURE", "1")
	defer os.Unsetenv("SESSIONNODE_ALLOW_INSECURE")

	validatePublicAccess("0.0.0.0:8080", "")
	// Should not call log.Fatalf
}

func TestValidatePublicAccess_LoopbackNoToken(t *testing.T) {
	// Loopback without token → no failure (local dev)
	validatePublicAccess("127.0.0.1:8080", "")
	// Should not call log.Fatalf
}

func TestValidatePublicAccess_EmptyAddrNoToken(t *testing.T) {
	// Empty address → no failure
	validatePublicAccess("", "")
	// Should not call log.Fatalf
}

func TestValidatePublicAccess_LocalhostNoToken(t *testing.T) {
	// localhost without token → no failure
	validatePublicAccess("localhost:8080", "")
	// Should not call log.Fatalf
}
