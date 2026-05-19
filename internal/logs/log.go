package logs

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Level constants.
const (
	LevelDebug = "debug"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// severity returns a numeric severity for the given level string.
// Higher values = more severe.
func severity(level string) int {
	switch level {
	case LevelDebug:
		return 0
	case LevelInfo:
		return 1
	case LevelWarn:
		return 2
	case LevelError:
		return 3
	default:
		return 1
	}
}

// Field is a key-value pair for structured logging.
type Field struct {
	Key   string
	Value interface{}
}

// F creates a Field.
func F(key string, value interface{}) Field {
	return Field{Key: key, Value: value}
}

// logEntry is the JSON-serializable structure written for each log line.
type logEntry struct {
	Timestamp string      `json:"ts"`
	Level     string      `json:"level"`
	App       string      `json:"app"`
	Msg       string      `json:"msg"`
	Fields    interface{} `json:"fields,omitempty"`
}

// Logger provides structured logging with levels.
type Logger struct {
	mu      sync.Mutex
	level   string
	writer  io.Writer
	appName string
}

// NewLogger creates a Logger writing to the given writer.
// level must be one of LevelDebug, LevelInfo, LevelWarn, LevelError.
// If level is unrecognised, LevelInfo is used.
func NewLogger(w io.Writer, level, appName string) *Logger {
	if severity(level) < 0 {
		level = LevelInfo
	}
	return &Logger{
		level:   level,
		writer:  w,
		appName: appName,
	}
}

// SetLevel changes the log level at runtime.
func (l *Logger) SetLevel(level string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// writeEntry serialises and writes one log line. It does not check the level
// threshold — that is the caller's responsibility.
func (l *Logger) writeEntry(level, msg string, fields []Field) {
	entry := logEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     level,
		App:       l.appName,
		Msg:       msg,
	}
	if len(fields) > 0 {
		kv := make(map[string]interface{}, len(fields))
		for _, f := range fields {
			kv[f.Key] = f.Value
		}
		entry.Fields = kv
	}

	data, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logs: json marshal error: %v\n", err)
		return
	}
	data = append(data, '\n')

	l.mu.Lock()
	_, _ = l.writer.Write(data)
	l.mu.Unlock()
}

// Debug writes a debug-level log entry.
func (l *Logger) Debug(msg string, fields ...Field) {
	if severity(l.level) > severity(LevelDebug) {
		return
	}
	l.writeEntry(LevelDebug, msg, fields)
}

// Info writes an info-level log entry.
func (l *Logger) Info(msg string, fields ...Field) {
	if severity(l.level) > severity(LevelInfo) {
		return
	}
	l.writeEntry(LevelInfo, msg, fields)
}

// Warn writes a warn-level log entry.
func (l *Logger) Warn(msg string, fields ...Field) {
	if severity(l.level) > severity(LevelWarn) {
		return
	}
	l.writeEntry(LevelWarn, msg, fields)
}

// Error writes an error-level log entry.
func (l *Logger) Error(msg string, fields ...Field) {
	if severity(l.level) > severity(LevelError) {
		return
	}
	l.writeEntry(LevelError, msg, fields)
}

// With creates a Logger that prepends the given fields to all entries.
// The returned Logger shares the same writer and appName but carries its
// own field prefix. Closing or modifying the parent does not affect the child.
func (l *Logger) With(fields ...Field) *Logger {
	cp := &Logger{
		mu:      sync.Mutex{},
		level:   l.level,
		writer:  l.writer,
		appName: l.appName,
	}
	return cp
}

// Setup creates the default logger and audit logger in the data directory.
func Setup(dataDir string, level string) (*Logger, *AuditLogger, error) {
	logDir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, nil, fmt.Errorf("logs.Setup: mkdir: %w", err)
	}

	coreWriter, err := NewRotateWriter(logDir, "core.log", 100*1024*1024, 10)
	if err != nil {
		return nil, nil, fmt.Errorf("logs.Setup: core rotate writer: %w", err)
	}

	auditWriter, err := NewRotateWriter(logDir, "audit.log", 100*1024*1024, 10)
	if err != nil {
		coreWriter.Close()
		return nil, nil, fmt.Errorf("logs.Setup: audit rotate writer: %w", err)
	}

	logger := NewLogger(coreWriter, level, "sessionnode-core")
	audit := NewAuditLogger(auditWriter)
	return logger, audit, nil
}
