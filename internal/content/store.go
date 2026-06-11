// Package content implements a content-addressable store with reference counting.
//
// Every blob is stored at trash/<hash[:2]>/<hash> and content is deduplicated:
// the same SHA256 written 10 times only occupies physical space once.
//
// Reference lifecycle:
//   - Store(data) → creates blob or increments refCount
//   - Release(hash) → decrements refCount
//   - GC() → removes blobs with refCount == 0
//
// Design notes:
//   - Blobs are stored with mode 0400 (read-only) to prevent accidental modification
//   - Ref counts are persisted alongside the blob directory in .refs file
//   - Blobs larger than MaxArtifactSize (1MB) are rejected — caller must handle
//     large files as B-class artifacts instead
package content

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/PENG1028/sessionbridge-core/pkg/types"
)

// MaxArtifactSize is the maximum blob size stored in the Content Store.
// Files larger than this are rejected; the caller should treat them as
// B-class artifacts (metadata-only).
const MaxArtifactSize = 1 * 1024 * 1024 // 1 MB

// ErrTooLarge is returned when content exceeds MaxArtifactSize.
var ErrTooLarge = fmt.Errorf("content exceeds max artifact size (%d bytes)", MaxArtifactSize)

// Store is a content-addressable, reference-counted blob store.
// Safe for concurrent use.
type Store struct {
	baseDir string // e.g. ~/.sessionnode/trash/
	mu      sync.Mutex
	refs    map[string]int64 // hash → refCount (loaded from disk)
	dirty   bool
}

// NewStore creates a Content Store rooted at the given directory.
func NewStore(dir string) *Store {
	return &Store{
		baseDir: dir,
		refs:    make(map[string]int64),
	}
}

// Load reads persisted ref counts from disk.
// Must be called once after creation.
func (cs *Store) Load() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	refPath := cs.refFilePath()
	data, err := os.ReadFile(refPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // fresh store
		}
		return fmt.Errorf("content load refs: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		count, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			continue
		}
		cs.refs[parts[0]] = count
	}
	return nil
}

// Store saves content into the store and returns a ContentRef.
// If the content already exists (same hash), increments the ref count.
// Returns ErrTooLarge if content exceeds MaxArtifactSize.
func (cs *Store) Store(data []byte) (*types.ContentRef, error) {
	if len(data) > MaxArtifactSize {
		return nil, ErrTooLarge
	}

	hash := sha256Hex(data)
	dir := filepath.Join(cs.baseDir, hash[:2])
	path := filepath.Join(dir, hash)

	cs.mu.Lock()
	defer cs.mu.Unlock()

	// Check if blob already exists
	if _, err := os.Stat(path); err == nil {
		cs.refs[hash]++
		cs.dirty = true
	} else if os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("content mkdir: %w", err)
		}
		if err := os.WriteFile(path, data, 0400); err != nil {
			return nil, fmt.Errorf("content write: %w", err)
		}
		cs.refs[hash] = 1
		cs.dirty = true
	} else {
		return nil, fmt.Errorf("content stat: %w", err)
	}

	return &types.ContentRef{
		Hash:     hash,
		Size:     int64(len(data)),
		StoredAt: path,
	}, nil
}

// Restore reads content by ContentRef and returns the raw bytes.
// Returns nil if the blob does not exist.
func (cs *Store) Restore(ref *types.ContentRef) ([]byte, error) {
	data, err := os.ReadFile(ref.StoredAt)
	if err != nil {
		if os.IsNotExist(err) {
			// Fallback: try to locate by hash
			hashPath := filepath.Join(cs.baseDir, ref.Hash[:2], ref.Hash)
			data, err = os.ReadFile(hashPath)
			if err != nil {
				return nil, fmt.Errorf("content restore: file not found at %s or %s", ref.StoredAt, hashPath)
			}
		} else {
			return nil, fmt.Errorf("content restore: %w", err)
		}
	}
	return data, nil
}

// Release decrements the reference count for the given hash.
// Does not remove the blob — call GC() to reclaim space.
func (cs *Store) Release(hash string) int64 {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.refs[hash]--
	cs.dirty = true
	return cs.refs[hash]
}

// RefCount returns the current reference count for a hash.
func (cs *Store) RefCount(hash string) int64 {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.refs[hash]
}

// GCResult summarizes a garbage collection run.
type GCResult struct {
	Removed int   // number of blobs removed
	Freed   int64 // bytes reclaimed
}

// GC removes all blobs with refCount <= 0 and persists the remaining refs.
func (cs *Store) GC() GCResult {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	result := GCResult{}

	for hash, count := range cs.refs {
		if count > 0 {
			continue
		}
		path := filepath.Join(cs.baseDir, hash[:2], hash)
		if info, err := os.Stat(path); err == nil {
			result.Freed += info.Size()
			os.Remove(path)
			result.Removed++
		}
		delete(cs.refs, hash)
	}

	// Persist remaining refs
	cs.persistLocked()
	cs.dirty = false
	return result
}

// Persist writes the ref counts to disk.
func (cs *Store) Persist() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.persistLocked()
}

// --- internal ---

func (cs *Store) refFilePath() string {
	return filepath.Join(cs.baseDir, ".refs")
}

func (cs *Store) persistLocked() error {
	if !cs.dirty && fileExists(cs.refFilePath()) {
		return nil
	}
	var b strings.Builder
	hashes := make([]string, 0, len(cs.refs))
	for h := range cs.refs {
		hashes = append(hashes, h)
	}
	sort.Strings(hashes)
	for _, h := range hashes {
		if cs.refs[h] > 0 {
			fmt.Fprintf(&b, "%s %d\n", h, cs.refs[h])
		}
	}
	path := cs.refFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
