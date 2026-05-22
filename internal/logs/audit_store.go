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

// AuditStore is a thread-safe in-memory store for audit records.
type AuditStore struct {
	mu      sync.RWMutex
	records []AuditRecord
}

// NewAuditStore creates an empty audit store.
func NewAuditStore() *AuditStore {
	return &AuditStore{}
}

// Record adds an audit record. If AuditID is empty, a random one is generated.
func (s *AuditStore) Record(r AuditRecord) {
	if r.AuditID == "" {
		b := make([]byte, 8)
		rand.Read(b)
		r.AuditID = hex.EncodeToString(b)
	}

	s.mu.Lock()
	s.records = append(s.records, r)
	s.mu.Unlock()
}

// List returns audit records matching the given filters, up to limit.
// limit is clamped to [1, 1000] with a default of 100.
// Returns a copy of the internal data.
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
	// Walk from most recent to oldest.
	for i := len(s.records) - 1; i >= 0; i-- {
		r := s.records[i]
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
