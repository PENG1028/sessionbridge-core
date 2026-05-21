package history

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/user/sessionnode/go-core/pkg/types"
)

// defaultBaseDir is the default directory for disk-mode session history.
const defaultBaseDir = "~/.sessionnode/sessions"

// truncationMarker is a synthetic event inserted when maxBytes forces truncation.
var truncationMarker = types.HistoryEvent{
	Type: "history.truncated",
}

// sessionHistory holds all retained events for a single session.
type sessionHistory struct {
	mu      sync.RWMutex
	policy  types.HistoryPolicy
	events  []types.HistoryEvent
	bytes   int64 // total bytes of event data stored
	dropped int64 // bytes dropped due to maxBytes
	truncated bool
	fromSeq types.EventSeq
	nextSeq types.EventSeq
	dir     string // on-disk directory; empty = memory mode

	// file handles for disk mode (lazily opened)
	stdoutFile *os.File
	stderrFile *os.File
	eventsFile *os.File
}

// PluginEvent records a plugin lifecycle event (enable, disable, permission grant, etc.).
type PluginEvent struct {
	PluginID  string      `json:"pluginId"`
	EventType string      `json:"eventType"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp int64       `json:"timestamp"`
}

// Store manages session history for all sessions.
// It is safe for concurrent use.
type Store struct {
	mu           sync.RWMutex
	sessions     map[types.SessionID]*sessionHistory
	baseDir      string
	pluginEvents map[string][]PluginEvent // pluginID -> events
}

// New creates a history Store with the given base directory for disk mode.
// An empty baseDir defaults to ~/.sessionnode/sessions.
func New(baseDir string) *Store {
	if baseDir == "" {
		baseDir = defaultBaseDir
	}
	return &Store{
		sessions:     make(map[types.SessionID]*sessionHistory),
		baseDir:      baseDir,
		pluginEvents: make(map[string][]PluginEvent),
	}
}

// InitSession creates history tracking for a session.
// Must be called after the session is created.
func (s *Store) InitSession(sid types.SessionID, policy types.HistoryPolicy) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.sessions[sid]; exists {
		return nil // already initialized
	}

	sh := &sessionHistory{
		policy:  policy,
		fromSeq: 1,
		nextSeq: 1,
	}

	if !policy.Enabled {
		s.sessions[sid] = sh
		return nil
	}

	// Disk mode: create directory and files
	if policy.Mode == types.HistoryModeDisk {
		dir := filepath.Join(s.baseDir, string(sid))
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("create history dir: %w", err)
		}
		sh.dir = dir
	}

	s.sessions[sid] = sh
	return nil
}

// Record writes a stream data event into the session's history.
// It is a no-op if history is disabled or the stream type is not in policy.Streams.
func (s *Store) Record(sid types.SessionID, streamType string, seq types.EventSeq, data string) {
	s.mu.RLock()
	sh, ok := s.sessions[sid]
	s.mu.RUnlock()
	if !ok {
		return
	}

	sh.mu.Lock()
	defer sh.mu.Unlock()

	if !sh.policy.Enabled {
		return
	}

	// Check if this stream type is in the policy
	if !sh.trackStream(streamType) {
		return
	}

	// Stdin redaction: never store raw stdin data in history.
	// This is a defense-in-depth measure — even if a caller explicitly
	// includes "stdin" in policy.Streams, the raw content is replaced.
	// The event metadata (timestamp, seq, stream type) is still recorded
	// so replay consumers can detect that stdin activity occurred.
	if streamType == "stdin" {
		data = "[stdin redacted]"
	}

	evt := types.HistoryEvent{
		EventSeq:  seq,
		Type:      "stream." + streamType,
		Stream:    streamType,
		Data:      data,
		Timestamp: time.Now().UnixMilli(),
	}

	// Check and enforce maxBytes
	dataLen := int64(len(data))
	if sh.bytes+dataLen > sh.policy.MaxBytes {
		s.truncateLocked(sh, dataLen)
	}

	sh.events = append(sh.events, evt)
	sh.bytes += dataLen
	if seq >= sh.nextSeq {
		sh.nextSeq = seq + 1
	}

	// Disk mode: write to files
	if sh.dir != "" && sh.policy.Mode == types.HistoryModeDisk {
		s.writeDiskLocked(sh, streamType, data, evt)
	}
}

// RecordEvent records a lifecycle event (session.created, session.stopped, etc.)
func (s *Store) RecordEvent(sid types.SessionID, seq types.EventSeq, eventType string, payload interface{}) {
	s.mu.RLock()
	sh, ok := s.sessions[sid]
	s.mu.RUnlock()
	if !ok {
		return
	}

	sh.mu.Lock()
	defer sh.mu.Unlock()

	if !sh.policy.Enabled {
		return
	}

	evt := types.HistoryEvent{
		EventSeq:  seq,
		Type:      eventType,
		Timestamp: time.Now().UnixMilli(),
	}

	if eventType == "exited" {
		if m, ok := payload.(map[string]interface{}); ok {
			if ec, ok := m["exitCode"]; ok {
				if code, ok := ec.(int); ok {
					evt.ExitCode = code
				}
			}
		}
	}

	sh.events = append(sh.events, evt)
	if seq >= sh.nextSeq {
		sh.nextSeq = seq + 1
	}

	if sh.dir != "" && sh.policy.Mode == types.HistoryModeDisk {
		s.writeEventJSONLocked(sh, evt)
	}
}

// Replay returns all events for the given session and stream type from fromSeq onward.
// Returns (events, nil) if successful.
// Returns (nil, ErrHistoryDisabled) if history is disabled.
// Returns (nil, ErrRangeTruncated) if fromSeq is before the earliest available event.
// Returns (nil, ErrNotAvailable) for memory mode after restart (not detectable here).
func (s *Store) Replay(sid types.SessionID, streamType string, fromSeq types.EventSeq) ([]types.HistoryEvent, error) {
	s.mu.RLock()
	sh, ok := s.sessions[sid]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sid)
	}

	sh.mu.RLock()
	defer sh.mu.RUnlock()

	if !sh.policy.Enabled {
		return nil, NewHistoryDisabledError()
	}

	if fromSeq < sh.fromSeq && sh.truncated {
		// Return available range from current fromSeq
		events := s.filterEventsLocked(sh, streamType, sh.fromSeq)
		return events, NewRangeTruncatedError(sh.fromSeq, sh.nextSeq)
	}

	events := s.filterEventsLocked(sh, streamType, fromSeq)
	return events, nil
}

// Tail returns the last N events for the given session and stream type.
func (s *Store) Tail(sid types.SessionID, streamType string, lines int) ([]types.HistoryEvent, error) {
	s.mu.RLock()
	sh, ok := s.sessions[sid]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sid)
	}

	sh.mu.RLock()
	defer sh.mu.RUnlock()

	if !sh.policy.Enabled {
		return nil, NewHistoryDisabledError()
	}

	var filtered []types.HistoryEvent
	for _, evt := range sh.events {
		if streamType == "" || evt.Stream == streamType || (streamType == "event" && evt.Stream == "") {
			filtered = append(filtered, evt)
		}
	}

	if lines <= 0 || lines >= len(filtered) {
		return filtered, nil
	}
	return filtered[len(filtered)-lines:], nil
}

// Stats returns summary information about the session's history.
func (s *Store) Stats(sid types.SessionID) (*types.HistoryStats, error) {
	s.mu.RLock()
	sh, ok := s.sessions[sid]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sid)
	}

	sh.mu.RLock()
	defer sh.mu.RUnlock()

	return &types.HistoryStats{
		SessionID:    sid,
		Mode:         sh.policy.Mode,
		EventCount:   int64(len(sh.events)),
		BytesStored:  sh.bytes,
		BytesDropped: sh.dropped,
		Truncated:    sh.truncated,
		FromSeq:      sh.fromSeq,
		NextSeq:      sh.nextSeq,
	}, nil
}

// Clear removes all history for the given session and optionally specific streams.
// Returns the estimated bytes freed.
func (s *Store) Clear(sid types.SessionID, streams []string) (int64, error) {
	s.mu.Lock()
	sh, ok := s.sessions[sid]
	s.mu.Unlock()
	if !ok {
		return 0, fmt.Errorf("session not found: %s", sid)
	}

	sh.mu.Lock()
	defer sh.mu.Unlock()

	if !sh.policy.Enabled {
		return 0, nil
	}

	bytesFreed := sh.bytes

	if len(streams) == 0 {
		// Clear all
		sh.events = nil
		sh.bytes = 0
		sh.dropped = 0
		sh.truncated = false
		sh.fromSeq = sh.nextSeq
		sh.removeDiskFilesLocked()
	} else {
		streamSet := make(map[string]bool)
		for _, st := range streams {
			streamSet[st] = true
		}
		var kept []types.HistoryEvent
		var keptBytes int64
		for _, evt := range sh.events {
			if streamSet[evt.Stream] {
				continue
			}
			kept = append(kept, evt)
			keptBytes += int64(len(evt.Data))
		}
		bytesFreed = sh.bytes - keptBytes
		sh.events = kept
		sh.bytes = keptBytes
		// Recalculate fromSeq
		if len(kept) > 0 {
			sh.fromSeq = kept[0].EventSeq
		} else {
			sh.fromSeq = sh.nextSeq
		}
	}

	return bytesFreed, nil
}

// RemoveSession removes all history tracking for a session.
func (s *Store) RemoveSession(sid types.SessionID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sh, ok := s.sessions[sid]; ok {
		sh.mu.Lock()
		sh.closeFilesLocked()
		sh.mu.Unlock()
	}
	delete(s.sessions, sid)
}

// Cleanup removes all sessions and closes all files.
func (s *Store) Cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, sh := range s.sessions {
		sh.mu.Lock()
		sh.closeFilesLocked()
		sh.mu.Unlock()
	}
	s.sessions = make(map[types.SessionID]*sessionHistory)
}

// --- Plugin event support ---

// RecordPluginEvent records a plugin lifecycle event.
func (s *Store) RecordPluginEvent(pluginID string, eventType string, data interface{}) {
	evt := PluginEvent{
		PluginID:  pluginID,
		EventType: eventType,
		Data:      data,
		Timestamp: time.Now().UnixMilli(),
	}
	s.mu.Lock()
	s.pluginEvents[pluginID] = append(s.pluginEvents[pluginID], evt)
	s.mu.Unlock()
}

// QueryPluginEvents returns all recorded events for the given plugin.
func (s *Store) QueryPluginEvents(pluginID string) []PluginEvent {
	s.mu.RLock()
	events := s.pluginEvents[pluginID]
	s.mu.RUnlock()
	if events == nil {
		return []PluginEvent{}
	}
	out := make([]PluginEvent, len(events))
	copy(out, events)
	return out
}

// --- Log / Audit stubs (Phase 1: return nil — callers handle gracefully) ---

// RecentLogLines returns log lines matching source and level, up to limit.
// Phase 1: returns nil (callers treat nil as empty slice).
func (s *Store) RecentLogLines(source, level string, limit int) interface{} {
	return nil
}

// RecentLogEntries returns structured log entries matching source, pluginID and level.
// Phase 1: returns nil.
func (s *Store) RecentLogEntries(source, pluginID, level string, limit int) interface{} {
	return nil
}

// RecentAuditEntries returns audit trail entries matching the given filters.
// Phase 1: returns nil.
func (s *Store) RecentAuditEntries(eventType, actor, target string, limit int) interface{} {
	return nil
}

// --- internal helpers ---

func (sh *sessionHistory) trackStream(streamType string) bool {
	for _, s := range sh.policy.Streams {
		if s == streamType {
			return true
		}
	}
	return false
}

func (s *Store) truncateLocked(sh *sessionHistory, newDataLen int64) {
	// Remove oldest events until there's room
	for len(sh.events) > 0 && sh.bytes+newDataLen > sh.policy.MaxBytes {
		evict := sh.events[0]
		evictLen := int64(len(evict.Data))
		sh.bytes -= evictLen
		sh.dropped += evictLen
		sh.events = sh.events[1:]
	}

	sh.truncated = true
	if len(sh.events) > 0 {
		sh.fromSeq = sh.events[0].EventSeq
	} else {
		// All events evicted — insert truncation marker
		sh.fromSeq = sh.nextSeq
		sh.events = append(sh.events, truncationMarker)
		sh.events = append(sh.events, types.HistoryEvent{
			EventSeq: sh.nextSeq,
			Type:     "history.continued",
			Timestamp: time.Now().UnixMilli(),
		})
		sh.nextSeq++
	}
}

func (s *Store) filterEventsLocked(sh *sessionHistory, streamType string, fromSeq types.EventSeq) []types.HistoryEvent {
	var out []types.HistoryEvent
	for _, evt := range sh.events {
		if evt.EventSeq < fromSeq {
			continue
		}
		if streamType == "" || streamType == "event" {
			out = append(out, evt)
		} else if evt.Stream == streamType {
			out = append(out, evt)
		}
	}
	return out
}

func (s *Store) writeDiskLocked(sh *sessionHistory, streamType, data string, evt types.HistoryEvent) {
	// Write raw data to stream-specific log file
	var f **os.File
	switch streamType {
	case "stdout":
		f = &sh.stdoutFile
	case "stderr":
		f = &sh.stderrFile
	default:
		return
	}
	if *f == nil {
		var err error
		*f, err = os.OpenFile(filepath.Join(sh.dir, streamType+".log"),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return
		}
	}
	(*f).WriteString(data)

	// Write structured event to events.jsonl
	s.writeEventJSONLocked(sh, evt)
}

func (s *Store) writeEventJSONLocked(sh *sessionHistory, evt types.HistoryEvent) {
	if sh.eventsFile == nil {
		var err error
		sh.eventsFile, err = os.OpenFile(filepath.Join(sh.dir, "events.jsonl"),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return
		}
	}
	line, _ := json.Marshal(evt)
	sh.eventsFile.Write(line)
	sh.eventsFile.WriteString("\n")
}

func (sh *sessionHistory) closeFilesLocked() {
	for _, f := range []*os.File{sh.stdoutFile, sh.stderrFile, sh.eventsFile} {
		if f != nil {
			f.Close()
		}
	}
	sh.stdoutFile = nil
	sh.stderrFile = nil
	sh.eventsFile = nil
}

func (sh *sessionHistory) removeDiskFilesLocked() {
	sh.closeFilesLocked()
	if sh.dir != "" {
		os.RemoveAll(sh.dir)
	}
}

// --- Error helpers ---

// Error types for history operations.
var (
	errHistoryDisabled = &HistoryError{code: "HISTORY_DISABLED", message: "session history is disabled"}
	errRangeTruncated  = &RangeTruncatedError{message: "requested range has been truncated"}
)

type HistoryError struct {
	code    string
	message string
}

func (e *HistoryError) Error() string  { return e.message }
func (e *HistoryError) Code() string    { return e.code }

type RangeTruncatedError struct {
	FromSeq types.EventSeq
	NextSeq types.EventSeq
	message string
}

func (e *RangeTruncatedError) Error() string { return e.message }
func (e *RangeTruncatedError) Code() string  { return "HISTORY_RANGE_TRUNCATED" }

func NewHistoryDisabledError() error {
	return &HistoryError{code: "HISTORY_DISABLED", message: "session history is disabled"}
}

func NewRangeTruncatedError(fromSeq, nextSeq types.EventSeq) error {
	return &RangeTruncatedError{
		FromSeq: fromSeq,
		NextSeq: nextSeq,
		message: fmt.Sprintf("requested range has been truncated, available from seq %d", fromSeq),
	}
}

func NewNotAvailableError() error {
	return &HistoryError{code: "HISTORY_NOT_AVAILABLE", message: "history not available (memory mode, restarted)"}
}

// IsHistoryDisabled checks if an error is HISTORY_DISABLED.
func IsHistoryDisabled(err error) bool {
	if e, ok := err.(*HistoryError); ok && e.code == "HISTORY_DISABLED" {
		return true
	}
	return false
}

// IsRangeTruncated checks if an error is HISTORY_RANGE_TRUNCATED.
func IsRangeTruncated(err error) bool {
	_, ok := err.(*RangeTruncatedError)
	return ok
}

// ReplayFromDisk rebuilds in-memory history from disk for a session.
// Used after core restart when mode=disk.
func (s *Store) ReplayFromDisk(sid types.SessionID) error {
	s.mu.RLock()
	sh, ok := s.sessions[sid]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("session not found: %s", sid)
	}

	sh.mu.Lock()
	defer sh.mu.Unlock()

	if sh.dir == "" {
		return nil // not disk mode
	}

	eventsFile := filepath.Join(sh.dir, "events.jsonl")
	f, err := os.Open(eventsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no history yet
		}
		return fmt.Errorf("open events.jsonl: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var evt types.HistoryEvent
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}
		sh.events = append(sh.events, evt)
		sh.bytes += int64(len(evt.Data))
		if evt.EventSeq >= sh.nextSeq {
			sh.nextSeq = evt.EventSeq + 1
		}
	}

	if len(sh.events) > 0 {
		sh.fromSeq = sh.events[0].EventSeq
	}
	return nil
}
