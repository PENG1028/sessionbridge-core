package run

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/user/sessionnode/go-core/pkg/types"
)

// Store is an in-memory, thread-safe index of Run entries.
// When savePath is set, all mutations are automatically persisted to disk.
type Store struct {
	mu       sync.RWMutex
	runs     map[string]*Run
	counter  atomic.Int64
	savePath string
}

// NewStore creates an empty Run store.
func NewStore() *Store {
	return &Store{
		runs: make(map[string]*Run),
	}
}

// nextRunID generates a stable run ID.
func (s *Store) nextRunID() string {
	n := s.counter.Add(1)
	return fmt.Sprintf("run_%d_%d", time.Now().UnixMilli(), n)
}

// Create creates a new Run entry and returns it.
func (s *Store) Create(run *Run) *Run {
	s.mu.Lock()
	defer s.mu.Unlock()

	run.RunID = s.nextRunID()
	now := time.Now().UnixMilli()
	run.CreatedAt = now
	run.UpdatedAt = now
	if run.State == "" {
		run.State = StateRunning
	}
	if run.Metadata == nil {
		run.Metadata = make(map[string]string)
	}

	s.runs[run.RunID] = run
	s.persistLocked()
	return run
}

// Get retrieves a Run by ID. Returns nil if not found.
func (s *Store) Get(runID string) *Run {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.runs[runID]
	if !ok {
		return nil
	}
	// Return a copy to avoid data races on Metadata map.
	cp := *r
	cp.Metadata = make(map[string]string, len(r.Metadata))
	for k, v := range r.Metadata {
		cp.Metadata[k] = v
	}
	return &cp
}

// GetRef returns a direct reference (not a copy). Caller must hold lock or
// only read while not modifying. Exported for executor sync operations.
func (s *Store) GetRef(runID string) *Run {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runs[runID]
}

// FindBySessionID looks up a run by its SessionID or ProcessID.
// Returns nil if not found. Uses linear scan — run count is typically small.
func (s *Store) FindBySessionID(sid types.SessionID) *Run {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.runs {
		if r.SessionID == sid || r.ProcessID == sid {
			cp := *r
			cp.Metadata = make(map[string]string, len(r.Metadata))
			for k, v := range r.Metadata {
				cp.Metadata[k] = v
			}
			return &cp
		}
	}
	return nil
}

// List returns all runs, optionally filtered.
// All filter fields are AND-ed; zero values mean "no filter".
func (s *Store) List(kind, pluginID, state string) []*Run {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []*Run
	for _, r := range s.runs {
		if kind != "" && r.Kind != kind {
			continue
		}
		if pluginID != "" && string(r.PluginID) != pluginID {
			continue
		}
		if state != "" && r.State != state {
			continue
		}
		cp := *r
		cp.Metadata = make(map[string]string, len(r.Metadata))
		for k, v := range r.Metadata {
			cp.Metadata[k] = v
		}
		out = append(out, &cp)
	}
	if out == nil {
		out = []*Run{}
	}
	return out
}

// UpdateState changes the state of a run.
func (s *Store) UpdateState(runID, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.runs[runID]; ok {
		r.State = state
		r.UpdatedAt = time.Now().UnixMilli()
		s.persistLocked()
	}
}

// UpdatePolicy updates the policy of a run.
func (s *Store) UpdatePolicy(runID string, p Policy) error {
	if msg := ValidatePolicy(p); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return fmt.Errorf("run not found: %s", runID)
	}
	r.Policy = p
	r.UpdatedAt = time.Now().UnixMilli()
	s.persistLocked()
	return nil
}

// SaveProcessRef updates the process reference and state on a run.
func (s *Store) SaveProcessRef(runID string, sid types.SessionID, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.runs[runID]; ok {
		r.ProcessID = sid
		r.SessionID = sid
		r.State = state
		r.UpdatedAt = time.Now().UnixMilli()
		s.persistLocked()
	}
}

// Delete removes a run from the store. Returns false if not found.
func (s *Store) Delete(runID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.runs[runID]
	if ok {
		delete(s.runs, runID)
		s.persistLocked()
	}
	return ok
}

// Count returns the number of runs.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.runs)
}

// persistLocked writes the full run index to disk atomically.
// Caller must hold s.mu (write or read lock).
func (s *Store) persistLocked() {
	if s.savePath == "" {
		return
	}
	s.persist()
}
