// Package oplog implements an append-only, persistent Operation Log
// that serves as the single source of truth for all mutable state changes.
//
// File layout:
//
//	~/.sessionnode/oplog/
//	├── CHUNK_0000000001.jsonl    ← each chunk: 10000 JSON lines
//	├── CHUNK_0000000002.jsonl
//	└── ...
//
// Design principles:
//   - Append-only: once written, lines are never modified or deleted
//   - Truncation removes entire chunk files, never modifies remaining ones
//   - In-memory index (byID, byTime) is rebuilt from chunk files on startup
//   - Concurrent writes are protected by file-level locking
package oplog

import (
	"bufio"
	"encoding/json"
	"github.com/PENG1028/sessionbridge-core/internal/content"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PENG1028/sessionbridge-core/pkg/types"
)

// Defaults.
const (
	defaultBaseDir    = "oplog"
	defaultMaxRecords = 100000
	defaultChunkSize  = 10000
	defaultMaxChunks  = 50 // safety cap: prevents runaway chunk creation
)

// operationMeta is the in-memory index entry for one operation.
type operationMeta struct {
	OpID      types.OpID
	Class     types.OpClass
	Capability string
	Timestamp int64
	ChunkNo   int64 // chunk file sequence number
	Seq       int   // line offset within the chunk (0-based)
	Size      int64 // JSON line length (for seeking)
}

// Store is a persistent, append-only log of operations.
// Safe for concurrent use.
type Store struct {
	baseDir    string
	maxRecords int // total operations before truncation
	chunkSize  int // operations per chunk file
	mu         sync.RWMutex

	// Current write state
	curChunk int64   // current chunk number (0-based)
	curSeq   int     // next write position within current chunk
	curFile  *os.File // current chunk file handle

	// In-memory index — rebuilt from disk on Load()
	byID   map[types.OpID]*operationMeta
	byTime []*operationMeta // ordered by timestamp (for truncation and replay)
	total  int              // total operations across all chunks

	// Content is the content store for blob references.
	// When set, truncation releases ContentStore refs for removed ops.
	Content *content.Store
}

// StoreOption configures the Store.
type StoreOption func(*Store)

// WithMaxRecords sets the maximum number of operations before truncation.
func WithMaxRecords(n int) StoreOption {
	return func(s *Store) {
		if n > 0 {
			s.maxRecords = n
		}
	}
}

// WithChunkSize sets the number of operations per chunk file.
func WithChunkSize(n int) StoreOption {
	return func(s *Store) {
		if n > 0 {
			s.chunkSize = n
		}
	}
}

// NewStore creates an OpLog Store rooted at the given directory.
// The directory is created if it does not exist.
func NewStore(dir string, opts ...StoreOption) *Store {
	s := &Store{
		baseDir:    dir,
		maxRecords: defaultMaxRecords,
		chunkSize:  defaultChunkSize,
		byID:       make(map[types.OpID]*operationMeta),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// WithContentStore sets the Content Store for blob reference tracking.
// When set, OpLog truncation releases content references automatically.
func WithContentStore(cs *content.Store) StoreOption {
	return func(s *Store) {
		s.Content = cs
	}
}

// Load scans the chunk files on disk and rebuilds the in-memory index.
// Must be called once before Appending or Querying after creation/restart.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.baseDir, 0700); err != nil {
		return fmt.Errorf("oplog mkdir: %w", err)
	}

	// List existing chunk files
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return fmt.Errorf("oplog read dir: %w", err)
	}

	var maxChunk int64 = -1
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "CHUNK_") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		no, err := parseChunkNo(name)
		if err != nil {
			continue
		}
		if err := s.loadChunk(no); err != nil {
			return fmt.Errorf("oplog load chunk %d: %w", no, err)
		}
		if no > maxChunk {
			maxChunk = no
		}
	}

	// Set write state to continue after the last chunk
	if maxChunk >= 0 {
		s.curChunk = maxChunk
		s.curSeq = 0
		// Count lines in the current chunk to set curSeq
		chunkPath := chunkFilePath(s.baseDir, maxChunk)
		if lines, err := countLines(chunkPath); err == nil {
			s.curSeq = lines
		}
		// Open current chunk for appending
		s.curFile, _ = os.OpenFile(chunkPath, os.O_APPEND|os.O_WRONLY, 0644)
	}

	return nil
}

// Append records a new operation and returns its OpID.
func (s *Store) Append(op *types.Operation) (types.OpID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Generate OpID
	op.Timestamp = time.Now().UnixMilli()
	if op.OpID == "" {
		op.OpID = types.OpID(fmt.Sprintf("op_%d_%d", op.Timestamp, s.total+1))
	}

	// Serialize
	data, err := json.Marshal(op)
	if err != nil {
		return "", fmt.Errorf("oplog marshal: %w", err)
	}
	data = append(data, '\n')

	// Ensure chunk file is open
	if s.curFile == nil {
		if err := s.rotateChunk(); err != nil {
			return "", err
		}
	}

	// Check if we need to rotate
	if s.curSeq >= s.chunkSize {
		s.curFile.Close()
		s.curFile = nil
		if err := s.rotateChunk(); err != nil {
			return "", err
		}
	}

	// Write
	n, err := s.curFile.Write(data)
	if err != nil {
		return "", fmt.Errorf("oplog write: %w", err)
	}

	// Build index entry
	meta := &operationMeta{
		OpID:       op.OpID,
		Class:      op.Class,
		Capability:  op.Capability,
		Timestamp:  op.Timestamp,
		ChunkNo:    s.curChunk,
		Seq:        s.curSeq,
		Size:       int64(n),
	}

	s.byID[op.OpID] = meta
	s.byTime = append(s.byTime, meta)
	s.curSeq++
	s.total++

	// Truncate if over limit
	if s.total > s.maxRecords {
		s.truncateLocked()
	}

	return op.OpID, nil
}

// Get retrieves an operation by ID. Returns nil if not found.
func (s *Store) Get(opID types.OpID) *types.Operation {
	s.mu.RLock()
	defer s.mu.RUnlock()

	meta, ok := s.byID[opID]
	if !ok {
		return nil
	}
	op, _ := s.readOp(meta)
	return op
}

// Replay returns all operations from fromID (inclusive) to the end.
// If fromID is empty, replays from the beginning.
func (s *Store) Replay(fromID types.OpID) []*types.Operation {
	s.mu.RLock()
	defer s.mu.RUnlock()

	start := 0
	if fromID != "" {
		if _, ok := s.byID[fromID]; !ok {
			return nil
		}
		// Find position in byTime
		start = sort.Search(len(s.byTime), func(i int) bool {
			return s.byTime[i].OpID >= fromID
		})
	}

	var result []*types.Operation
	for i := start; i < len(s.byTime); i++ {
		op, err := s.readOp(s.byTime[i])
		if err != nil {
			continue
		}
		result = append(result, op)
	}
	return result
}

// Query returns operations matching the given filter.
func (s *Store) Query(filter types.OpFilter) []*types.Operation {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*types.Operation
	offset := filter.Offset

	for _, meta := range s.byTime {
		// Apply filters
		if len(filter.Classes) > 0 {
			matched := false
			for _, c := range filter.Classes {
				if meta.Class == c {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if filter.Capability != "" && meta.Capability != filter.Capability {
			continue
		}
		if filter.FromTime > 0 && meta.Timestamp < filter.FromTime {
			continue
		}
		if filter.ToTime > 0 && meta.Timestamp >= filter.ToTime {
			continue
		}

		// Read the full operation for field-level filters
		op, err := s.readOp(meta)
		if err != nil {
			continue
		}

		if filter.ActorType != "" && string(op.Actor.Type) != filter.ActorType {
			continue
		}
		if filter.ActorID != "" && string(op.Actor.ID) != filter.ActorID {
			continue
		}
		if filter.TargetNode != "" && op.TargetNode != filter.TargetNode {
			continue
		}
		if filter.SessionID != "" && op.SessionID != filter.SessionID {
			continue
		}
		if filter.Path != "" {
			matched := false
			if op.ContentBefore != nil && op.ContentBefore.Path == filter.Path {
				matched = true
			}
			if op.ContentAfter != nil && op.ContentAfter.Path == filter.Path {
				matched = true
			}
			for _, a := range op.Artifacts {
				if a.Path == filter.Path {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}

		if offset > 0 {
			offset--
			continue
		}

		result = append(result, op)
		if filter.Limit > 0 && len(result) >= filter.Limit {
			break
		}
	}

	return result
}

// Truncate removes the oldest operations, keeping only the most recent maxRecords.
func (s *Store) Truncate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.truncateLocked()
}

// Close closes the current chunk file.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.curFile != nil {
		return s.curFile.Close()
	}
	return nil
}

// --- internal helpers ---

func (s *Store) rotateChunk() error {
	s.curChunk++
	s.curSeq = 0
	path := chunkFilePath(s.baseDir, s.curChunk)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("oplog create chunk %d: %w", s.curChunk, err)
	}
	s.curFile = f
	return nil
}

func (s *Store) readOp(meta *operationMeta) (*types.Operation, error) {
	path := chunkFilePath(s.baseDir, meta.ChunkNo)
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Seek to the correct line using sequential scan from the start of the file.
	// For production, we'd store byte offsets for O(1) seek.
	scanner := bufio.NewScanner(f)
	line := 0
	for scanner.Scan() {
		if line == meta.Seq {
			var op types.Operation
			if err := json.Unmarshal(scanner.Bytes(), &op); err != nil {
				return nil, fmt.Errorf("oplog unmarshal: %w", err)
			}
			return &op, nil
		}
		line++
	}
	return nil, fmt.Errorf("oplog: operation %s not found at chunk %d seq %d", meta.OpID, meta.ChunkNo, meta.Seq)
}

func (s *Store) readOpFromDisk(meta *operationMeta) *types.Operation {
	path := chunkFilePath(s.baseDir, meta.ChunkNo)
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	line := 0
	for scanner.Scan() {
		if line == meta.Seq {
			var op types.Operation
			if err := json.Unmarshal(scanner.Bytes(), &op); err != nil {
				return nil
			}
			return &op
		}
		line++
	}
	return nil
}

func (s *Store) truncateLocked() {
	if s.total <= s.maxRecords {
		return
	}
	excess := s.total - s.maxRecords
	if excess <= 0 {
		return
	}

	// Collect chunks to remove (oldest first)
	chunkOps := make(map[int64]int) // chunkNo → count
	chunkOrder := make([]int64, 0)

	// Group byTime entries by chunk
	chunkFirst := make(map[int64]int) // chunkNo → first index in byTime
	for i, meta := range s.byTime {
		if _, exists := chunkOps[meta.ChunkNo]; !exists {
			chunkFirst[meta.ChunkNo] = i
			chunkOrder = append(chunkOrder, meta.ChunkNo)
		}
		chunkOps[meta.ChunkNo]++
	}

	// Remove oldest chunks until excess is covered
	removed := 0
	var removeChunks []int64
	for _, cn := range chunkOrder {
		if removed >= excess {
			break
		}
		count := chunkOps[cn]
		removed += count
		removeChunks = append(removeChunks, cn)
	}

	if len(removeChunks) == 0 {
		return
	}

	// Remove index entries for these chunks
	keep := make([]*operationMeta, 0, len(s.byTime))
	for _, meta := range s.byTime {
		shouldRemove := false
		for _, cn := range removeChunks {
			if meta.ChunkNo == cn {
				shouldRemove = true
				break
			}
		}
		if shouldRemove {
			delete(s.byID, meta.OpID)
		} else {
			keep = append(keep, meta)
		}
	}
	s.byTime = keep
	s.total = len(s.byTime)

	// Release ContentStore references for removed operations
	if s.Content != nil {
		for _, meta := range s.byTime {
			for _, cn := range removeChunks {
				if meta.ChunkNo == cn {
					if op := s.readOpFromDisk(meta); op != nil && op.ContentBefore != nil {
						s.Content.Release(op.ContentBefore.Hash)
					}
					break
				}
			}
		}
	}

	// Delete chunk files from disk
	for _, cn := range removeChunks {
		path := chunkFilePath(s.baseDir, cn)
		os.Remove(path)
	}
}

func (s *Store) loadChunk(chunkNo int64) error {
	path := chunkFilePath(s.baseDir, chunkNo)
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	seq := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			seq++
			continue
		}

		// Quick parse: extract opId and class without full unmarshal
		var partial struct {
			OpID       types.OpID    `json:"opId"`
			Class      types.OpClass `json:"class"`
			Capability string        `json:"capability"`
			Timestamp  int64         `json:"timestamp"`
		}
		if err := json.Unmarshal(line, &partial); err != nil {
			seq++
			continue
		}

		if partial.OpID == "" {
			seq++
			continue
		}

		meta := &operationMeta{
			OpID:       partial.OpID,
			Class:      partial.Class,
			Capability:  partial.Capability,
			Timestamp:  partial.Timestamp,
			ChunkNo:    chunkNo,
			Seq:        seq,
			Size:       int64(len(line)),
		}
		s.byID[partial.OpID] = meta
		s.byTime = append(s.byTime, meta)
		seq++
	}

	s.total += seq
	return nil
}

// --- package helpers ---

func chunkFilePath(dir string, no int64) string {
	return filepath.Join(dir, fmt.Sprintf("CHUNK_%010d.jsonl", no))
}

func parseChunkNo(name string) (int64, error) {
	// "CHUNK_0000000042.jsonl" → 42
	trimmed := strings.TrimPrefix(name, "CHUNK_")
	trimmed = strings.TrimSuffix(trimmed, ".jsonl")
	return strconv.ParseInt(trimmed, 10, 64)
}

func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		count++
	}
	return count, nil
}
