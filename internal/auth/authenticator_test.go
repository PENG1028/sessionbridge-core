package auth

import (
	"testing"

	"github.com/user/sessionnode/go-core/pkg/types"
)

func TestAuthenticator_DevMode(t *testing.T) {
	a := NewTokenAuthenticator("") // no token → dev mode
	actor, err := a.Authenticate(types.Actor{Type: "web", ID: "browser_abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if actor.ID != "browser_abc" {
		t.Errorf("ID = %q", actor.ID)
	}
}

func TestAuthenticator_DevModeEmptyID(t *testing.T) {
	a := NewTokenAuthenticator("")
	actor, err := a.Authenticate(types.Actor{Type: "cli", ID: ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if actor.ID != "local_dev" {
		t.Errorf("ID = %q, want local_dev", actor.ID)
	}
}

func TestAuthenticator_ValidToken(t *testing.T) {
	a := NewTokenAuthenticator("secret123")
	actor, err := a.Authenticate(types.Actor{Type: "web", ID: "browser_abc", Token: "secret123"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Token should be stripped after validation
	if actor.Token != "" {
		t.Error("token should be stripped after validation")
	}
}

func TestAuthenticator_MissingToken(t *testing.T) {
	a := NewTokenAuthenticator("secret123")
	_, err := a.Authenticate(types.Actor{Type: "web", ID: "browser_abc"})
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestAuthenticator_InvalidToken(t *testing.T) {
	a := NewTokenAuthenticator("secret123")
	_, err := a.Authenticate(types.Actor{Type: "web", ID: "browser_abc", Token: "wrong"})
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestAuthenticator_MissingActorID(t *testing.T) {
	a := NewTokenAuthenticator("secret123")
	_, err := a.Authenticate(types.Actor{Type: "web", Token: "secret123"})
	if err == nil {
		t.Fatal("expected error for missing actor ID")
	}
}

func TestAuthenticator_NodeType(t *testing.T) {
	a := NewTokenAuthenticator("secret123")
	actor, err := a.Authenticate(types.Actor{Type: "node", ID: "node_remote"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if actor.ID != "node_remote" {
		t.Errorf("ID = %q", actor.ID)
	}
}
