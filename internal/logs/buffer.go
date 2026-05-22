package logs

import "sync"

// Entry is a structured in-memory log record used by the ring buffer.
type Entry struct {
	Timestamp int64                  `json:"timestamp"`
	Level     string                 `json:"level"`
	Source    string                 `json:"source"`
	PluginID  string                 `json:"pluginId,omitempty"`
	SessionID string                 `json:"sessionId,omitempty"`
	Message   string                 `json:"message"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
}

// Buffer is a thread-safe ring buffer of log entries.
type Buffer struct {
	mu       sync.RWMutex
	entries  []Entry
	capacity int
	head     int // next write position
	size     int // current number of entries
}

// NewBuffer creates a ring buffer with the given capacity.
// If capacity <= 0, defaults to 1000.
func NewBuffer(capacity int) *Buffer {
	if capacity <= 0 {
		capacity = 1000
	}
	return &Buffer{
		entries:  make([]Entry, capacity),
		capacity: capacity,
	}
}

// Add appends an entry. If at capacity, the oldest entry is dropped.
func (b *Buffer) Add(e Entry) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.entries[b.head] = e
	b.head = (b.head + 1) % b.capacity
	if b.size < b.capacity {
		b.size++
	}
}

// Tail returns the most recent entries matching the given source and level,
// up to limit. limit is clamped to [1, 1000] with a default of 100.
// Returns a copy of the internal data.
func (b *Buffer) Tail(source, level string, limit int) []Entry {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	// Collect matching entries from oldest to newest.
	var matched []Entry
	for i := 0; i < b.size; i++ {
		idx := (b.head - b.size + i + b.capacity) % b.capacity
		e := b.entries[idx]
		if source != "" && e.Source != source {
			continue
		}
		if level != "" && e.Level != level {
			continue
		}
		matched = append(matched, e)
	}

	// Return last N.
	if len(matched) <= limit {
		return matched
	}
	return matched[len(matched)-limit:]
}

// Query returns entries matching source, pluginID, and level filters,
// up to limit. limit is clamped to [1, 1000] with a default of 100.
// Returns a copy of the internal data.
func (b *Buffer) Query(source, pluginID, level string, limit int) []Entry {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	var matched []Entry
	for i := 0; i < b.size; i++ {
		idx := (b.head - b.size + i + b.capacity) % b.capacity
		e := b.entries[idx]
		if source != "" && e.Source != source {
			continue
		}
		if pluginID != "" && e.PluginID != pluginID {
			continue
		}
		if level != "" && e.Level != level {
			continue
		}
		matched = append(matched, e)
		if len(matched) >= limit {
			break
		}
	}
	return matched
}
