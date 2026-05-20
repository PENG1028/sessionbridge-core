package session

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/user/sessionnode/go-core/pkg/types"
)

// Store is an in-memory session store. Safe for concurrent use.
type Store struct {
	mu       sync.RWMutex
	sessions map[types.SessionID]*Session
	counter  atomic.Int64
}

// NewStore creates an empty session store.
func NewStore() *Store {
	return &Store{
		sessions: make(map[types.SessionID]*Session),
	}
}

// nextID generates a unique SessionID.
func (s *Store) nextID() types.SessionID {
	n := s.counter.Add(1)
	return types.SessionID(fmt.Sprintf("sess_%d", n))
}

// Create adds a new session to the store and returns its ID.
func (s *Store) Create(pluginID types.PluginID, command, cwd string, createdAt int64) types.SessionID {
	return s.CreateWithPolicy(pluginID, command, cwd, createdAt, types.DefaultHistoryPolicy())
}

// CreateWithPolicy adds a new session with the given history policy.
func (s *Store) CreateWithPolicy(pluginID types.PluginID, command, cwd string, createdAt int64, hp types.HistoryPolicy) types.SessionID {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.nextID()
	sess := NewSessionWithPolicy(id, pluginID, command, cwd, createdAt, hp)
	s.sessions[id] = sess
	return id
}

// Get retrieves a session by ID. Returns nil if not found.
func (s *Store) Get(id types.SessionID) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

// Destroy removes a session from the store.
func (s *Store) Destroy(id types.SessionID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

// List returns all sessions.
func (s *Store) List() []*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		out = append(out, sess)
	}
	return out
}

// Count returns the number of sessions.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

// Cleanup removes all sessions from the store.
func (s *Store) Cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = make(map[types.SessionID]*Session)
}
