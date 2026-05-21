package auth

import (
	"errors"
	"os"

	"github.com/user/sessionnode/go-core/pkg/types"
)

// TokenAuthenticator validates actor credentials using a shared secret.
// In Phase 1, local actors are allowed freely; remote actors must present a valid token.
type TokenAuthenticator struct {
	secretToken string // set via env SESSIONNODE_TOKEN or config
}

// NewTokenAuthenticator creates an authenticator.
// Reads the shared secret from SESSIONNODE_TOKEN env var (or uses the provided token).
func NewTokenAuthenticator(token string) *TokenAuthenticator {
	if token == "" {
		token = os.Getenv("SESSIONNODE_TOKEN")
	}
	return &TokenAuthenticator{secretToken: token}
}

// Authenticate validates the actor and returns the resolved actor with defaults filled in.
func (a *TokenAuthenticator) Authenticate(actor types.Actor) (*types.Actor, error) {
	// node actor type is only set by server for authenticated peer connections
	// (via handlePeerWS handshake). Control WS clients cannot set actorType=node —
	// the server's dispatchAction rejects those messages with ErrCodeActorTypeNodeBlocked.
	if actor.Type == "node" {
		return &actor, nil
	}

	// If no token is configured, allow all (development mode).
	if a.secretToken == "" {
		resolved := actor
		if resolved.ID == "" {
			resolved.ID = "local_dev"
		}
		return &resolved, nil
	}

	// Token validation.
	if actor.Token == "" {
		return nil, errors.New("missing authentication token")
	}
	if actor.Token != a.secretToken {
		return nil, errors.New("invalid authentication token")
	}
	if actor.ID == "" {
		return nil, errors.New("actor ID is required when token authentication is enabled")
	}

	resolved := actor
	resolved.Token = "" // strip token after validation
	return &resolved, nil
}
