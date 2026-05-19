package logs

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// AuditEntry represents a single audit log record for a capability call.
type AuditEntry struct {
	Timestamp  int64  `json:"ts"`
	PluginID   string `json:"pluginId"`
	ActorType  string `json:"actorType"`
	ActorID    string `json:"actorId"`
	Capability string `json:"capability"`
	TargetNode string `json:"targetNode,omitempty"`
	Allowed    bool   `json:"allowed"`
	Detail     string `json:"detail,omitempty"`
	RequestID  string `json:"requestId,omitempty"`
}

// AuditLogger writes audit entries as newline-delimited JSON to an underlying
// writer. It is safe for concurrent use.
type AuditLogger struct {
	mu     sync.Mutex
	writer io.WriteCloser
	logger *Logger
}

// NewAuditLogger creates an AuditLogger. The caller is responsible for calling
// Close when done.
func NewAuditLogger(writer io.WriteCloser) *AuditLogger {
	return &AuditLogger{
		writer: writer,
	}
}

// Log writes a single audit entry as a JSON line.
func (a *AuditLogger) Log(entry AuditEntry) {
	if entry.Timestamp == 0 {
		entry.Timestamp = time.Now().UnixMilli()
	}
	data, err := json.Marshal(entry)
	if err != nil {
		// Fallback: write a minimal error entry so the caller knows something failed.
		fallback, _ := json.Marshal(map[string]interface{}{
			"ts":    time.Now().UnixMilli(),
			"error": "marshal failed: " + err.Error(),
		})
		fallback = append(fallback, '\n')
		a.mu.Lock()
		_, _ = a.writer.Write(fallback)
		a.mu.Unlock()
		return
	}
	data = append(data, '\n')

	a.mu.Lock()
	_, _ = a.writer.Write(data)
	a.mu.Unlock()
}

// Close flushes and closes the underlying writer.
func (a *AuditLogger) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.writer.Close()
}
