package run

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// persistedStore is the on-disk JSON shape.
type persistedStore struct {
	Runs    map[string]*Run `json:"runs"`
	Counter int64           `json:"counter"`
}

// SetSavePath enables file-backed persistence for the store.
// After this call, all mutations (Create, UpdateState, UpdatePolicy,
// SaveProcessRef, Delete) are automatically persisted to disk.
func (s *Store) SetSavePath(path string) {
	s.savePath = path
}

// NewStoreWithPath creates a Store and immediately loads persisted runs from disk.
// If the file does not exist, an empty store is returned.
func NewStoreWithPath(path string) (*Store, error) {
	s := NewStore()
	s.savePath = path
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return s, nil
	}
	if err := s.loadFromDisk(); err != nil {
		return nil, fmt.Errorf("load run store: %w", err)
	}
	return s, nil
}

// LoadFromDisk reads a previously persisted store from disk.
func LoadFromDisk(path string) (*Store, error) {
	return NewStoreWithPath(path)
}

// persist writes the full run index to disk atomically.
// Caller must hold s.mu (write or read lock).
func (s *Store) persist() {
	if s.savePath == "" {
		return
	}

	ps := persistedStore{
		Runs:    s.runs,
		Counter: s.counter.Load(),
	}

	data, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return
	}

	dir := filepath.Dir(s.savePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return
	}

	tmp := s.savePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return
	}
	os.Rename(tmp, s.savePath)
}

// loadFromDisk reads the persisted file and populates the in-memory store.
func (s *Store) loadFromDisk() error {
	data, err := os.ReadFile(s.savePath)
	if err != nil {
		return err
	}

	var ps persistedStore
	if err := json.Unmarshal(data, &ps); err != nil {
		return fmt.Errorf("unmarshal run store: %w", err)
	}

	s.mu.Lock()
	s.runs = ps.Runs
	if s.runs == nil {
		s.runs = make(map[string]*Run)
	}
	s.mu.Unlock()

	// Recover counter from existing run IDs by scanning for the highest
	// sequence number embedded in run_<ts>_<seq> format.
	maxSeq := ps.Counter
	for id := range ps.Runs {
		if n := parseRunSeq(id); n > maxSeq {
			maxSeq = n
		}
	}
	s.counter.Store(maxSeq)

	return nil
}

// parseRunSeq extracts the sequence number from a run_<ts>_<seq> ID.
func parseRunSeq(id string) int64 {
	// Format: run_<millis>_<counter>
	parts := strings.Split(id, "_")
	if len(parts) < 3 {
		return 0
	}
	n, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	if err != nil {
		return 0
	}
	return n
}
