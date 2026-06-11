package logs

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// AuditRecord is an in-memory audit trail entry.
type AuditRecord struct {
	AuditID   string                 `json:"auditId"`
	Timestamp int64                  `json:"timestamp"`
	EventType string                 `json:"eventType"`
	Actor     string                 `json:"actor"`
	Target    string                 `json:"target"`
	Outcome   string                 `json:"outcome"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// defaultAuditCapacity is the default maximum number of records kept in memory.
const defaultAuditCapacity = 10000

// AuditStore is a thread-safe ring buffer for audit records.
// When at capacity, the oldest record is silently dropped.
type AuditStore struct {
	mu       sync.RWMutex
	records  []AuditRecord
	capacity int
	head     int // next write position
	size     int // current number of entries
}

// NewAuditStore creates an audit store with default capacity (10000).
func NewAuditStore() *AuditStore {
	return NewAuditStoreWithCapacity(defaultAuditCapacity)
}

// NewAuditStoreWithCapacity creates an audit store with the given capacity.
func NewAuditStoreWithCapacity(capacity int) *AuditStore {
	if capacity <= 0 {
		capacity = defaultAuditCapacity
	}
	return &AuditStore{
		records:  make([]AuditRecord, capacity),
		capacity: capacity,
	}
}

// Record adds an audit record. If AuditID is empty, a random one is generated.
// If at capacity, the oldest record is silently dropped.
func (s *AuditStore) Record(r AuditRecord) {
	if r.AuditID == "" {
		b := make([]byte, 8)
		rand.Read(b)
		r.AuditID = hex.EncodeToString(b)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.records[s.head] = r
	s.head = (s.head + 1) % s.capacity
	if s.size < s.capacity {
		s.size++
	}
}

// List returns audit records matching the given filters, up to limit.
// limit is clamped to [1, 1000] with a default of 100.
// Returns a copy of the internal data, most recent first.
func (s *AuditStore) List(eventType, actor, target string, limit int) []AuditRecord {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var matched []AuditRecord
	for i := s.size - 1; i >= 0; i-- {
		idx := (s.head - s.size + i + s.capacity) % s.capacity
		r := s.records[idx]
		if eventType != "" && r.EventType != eventType {
			continue
		}
		if actor != "" && r.Actor != actor {
			continue
		}
		if target != "" && r.Target != target {
			continue
		}
		matched = append(matched, r)
		if len(matched) >= limit {
			break
		}
	}
	return matched
}
