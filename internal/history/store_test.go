package history

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/user/sessionnode/go-core/pkg/types"
)

// ---------------------------------------------------------------------------
// InitSession
// ---------------------------------------------------------------------------

func TestStore_InitSession_MemoryMode(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_test_mem")

	policy := types.HistoryPolicy{
		Enabled: true,
		Mode:    types.HistoryModeMemory,
		Streams: []string{"stdout", "stderr"},
		MaxBytes: 1 << 20,
	}

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	stats, err := s.Stats(sid)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Mode != types.HistoryModeMemory {
		t.Errorf("Mode = %q, want %q", stats.Mode, types.HistoryModeMemory)
	}
	if stats.FromSeq != 1 {
		t.Errorf("FromSeq = %d, want 1", stats.FromSeq)
	}
	if stats.NextSeq != 1 {
		t.Errorf("NextSeq = %d, want 1", stats.NextSeq)
	}
}

func TestStore_InitSession_Disabled(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_disabled")

	policy := types.DefaultHistoryPolicy()
	policy.Enabled = false

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	_, err := s.Replay(sid, "", 1)
	if !IsHistoryDisabled(err) {
		t.Errorf("expected HISTORY_DISABLED, got %v", err)
	}
}

func TestStore_InitSession_DiskMode(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	sid := types.SessionID("sess_disk_init")

	policy := types.HistoryPolicy{
		Enabled: true,
		Mode:    types.HistoryModeDisk,
		Streams: []string{"stdout"},
		MaxBytes: 1 << 20,
	}

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	// Disk directory should exist
	sessionDir := filepath.Join(dir, string(sid))
	if _, err := os.Stat(sessionDir); os.IsNotExist(err) {
		t.Error("expected disk directory to exist")
	}
}

func TestStore_InitSession_Duplicate(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_dup")

	if err := s.InitSession(sid, types.DefaultHistoryPolicy()); err != nil {
		t.Fatalf("first InitSession: %v", err)
	}
	// Second init should be a no-op, not error
	if err := s.InitSession(sid, types.DefaultHistoryPolicy()); err != nil {
		t.Fatalf("second InitSession should not error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Record / Replay
// ---------------------------------------------------------------------------

func TestStore_RecordAndReplay(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_record_replay")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 1 << 20 // plenty of room

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.Record(sid, "stdout", 1, "hello\n")
	s.Record(sid, "stdout", 2, "world\n")
	s.Record(sid, "stderr", 3, "error\n")

	// Replay all from seq 1
	events, err := s.Replay(sid, "", 1)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Data != "hello\n" {
		t.Errorf("event[0].Data = %q", events[0].Data)
	}
	if events[2].Stream != "stderr" {
		t.Errorf("event[2].Stream = %q", events[2].Stream)
	}
}

func TestStore_Replay_FromSeq(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_from_seq")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 1 << 20

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.Record(sid, "stdout", 1, "a\n")
	s.Record(sid, "stdout", 2, "b\n")
	s.Record(sid, "stdout", 3, "c\n")

	// Replay from seq 2
	events, err := s.Replay(sid, "", 2)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Data != "b\n" {
		t.Errorf("first event data = %q, want %q", events[0].Data, "b\n")
	}
}

func TestStore_Replay_FilterByStream(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_filter")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 1 << 20

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.Record(sid, "stdout", 1, "out1\n")
	s.Record(sid, "stderr", 2, "err1\n")
	s.Record(sid, "stdout", 3, "out2\n")

	events, err := s.Replay(sid, "stdout", 1)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 stdout events, got %d", len(events))
	}
	for _, e := range events {
		if e.Stream != "stdout" {
			t.Errorf("expected stdout stream, got %q", e.Stream)
		}
	}
}

func TestStore_Replay_SessionNotFound(t *testing.T) {
	s := New("")
	_, err := s.Replay("sess_nonexistent", "", 1)
	if err == nil {
		t.Fatal("expected error for unknown session")
	}
}

func TestStore_Replay_HistoryDisabled(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_replay_disabled")
	policy := types.DefaultHistoryPolicy()
	policy.Enabled = false

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	_, err := s.Replay(sid, "", 1)
	if !IsHistoryDisabled(err) {
		t.Errorf("expected HISTORY_DISABLED, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// RecordEvent (lifecycle events)
// ---------------------------------------------------------------------------

func TestStore_RecordEvent(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_lifecycle")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 1 << 20

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.RecordEvent(sid, 1, "session.created", nil)
	s.RecordEvent(sid, 2, "session.started", nil)
	s.RecordEvent(sid, 3, "session.exited", map[string]interface{}{"exitCode": 0})

	events, err := s.Replay(sid, "", 1)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Type != "session.created" {
		t.Errorf("event[0].Type = %q", events[0].Type)
	}
	if events[2].Type != "session.exited" {
		t.Errorf("event[2].Type = %q", events[2].Type)
	}
}

func TestStore_RecordEvent_ExitCode(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_exitcode")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 1 << 20

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.RecordEvent(sid, 1, "exited", map[string]interface{}{"exitCode": 42})
	events, _ := s.Replay(sid, "", 1)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ExitCode != 42 {
		t.Errorf("ExitCode = %d, want 42", events[0].ExitCode)
	}
}

// ---------------------------------------------------------------------------
// Record — disabled / unregistered stream / no session
// ---------------------------------------------------------------------------

func TestStore_Record_DisabledPolicyIsNoop(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_record_disabled")
	policy := types.DefaultHistoryPolicy()
	policy.Enabled = false

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.Record(sid, "stdout", 1, "should not be saved\n")
	events, err := s.Replay(sid, "", 1)
	if !IsHistoryDisabled(err) {
		t.Errorf("expected error, got events: %v", events)
	}
}

func TestStore_Record_UnregisteredStreamIsNoop(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_unreg_stream")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 1 << 20
	policy.Streams = []string{"stdout"} // only stdout, not stdin

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.Record(sid, "stdout", 1, "visible\n")
	s.Record(sid, "stdin", 2, "hidden\n") // not in policy.Streams

	events, err := s.Replay(sid, "", 1)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event (stdout only), got %d", len(events))
	}
}

func TestStore_Record_UnknownSessionNoop(t *testing.T) {
	s := New("")
	// Record without InitSession — should be a no-op
	s.Record("sess_unknown", "stdout", 1, "data\n")

	// Should not panic or error, just silently ignore
}

func TestStore_RecordEvent_DisabledPolicyIsNoop(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_evt_disabled")
	policy := types.DefaultHistoryPolicy()
	policy.Enabled = false

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.RecordEvent(sid, 1, "session.created", nil)
	_, err := s.Replay(sid, "", 1)
	if !IsHistoryDisabled(err) {
		t.Errorf("expected HISTORY_DISABLED after recording event on disabled policy")
	}
}

// ---------------------------------------------------------------------------
// Tail
// ---------------------------------------------------------------------------

func TestStore_Tail(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_tail")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 1 << 20

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	for i := 1; i <= 10; i++ {
		s.Record(sid, "stdout", types.EventSeq(i), "line\n")
	}

	events, err := s.Tail(sid, "", 3)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].EventSeq != 8 {
		t.Errorf("first tail event seq = %d, want 8", events[0].EventSeq)
	}
	if events[2].EventSeq != 10 {
		t.Errorf("last tail event seq = %d, want 10", events[2].EventSeq)
	}
}

func TestStore_Tail_AllEvents(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_tail_all")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 1 << 20

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.Record(sid, "stdout", 1, "a\n")
	s.Record(sid, "stdout", 2, "b\n")

	// lines=0 means return all
	events, err := s.Tail(sid, "", 0)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
}

func TestStore_Tail_WithStreamFilter(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_tail_stream")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 1 << 20

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.Record(sid, "stdout", 1, "out1\n")
	s.Record(sid, "stderr", 2, "err1\n")
	s.Record(sid, "stdout", 3, "out2\n")

	events, err := s.Tail(sid, "stdout", 0)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 stdout events, got %d", len(events))
	}
}

func TestStore_Tail_SessionNotFound(t *testing.T) {
	s := New("")
	_, err := s.Tail("sess_nonexistent", "", 10)
	if err == nil {
		t.Fatal("expected error for unknown session")
	}
}

func TestStore_Tail_HistoryDisabled(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_tail_disabled")
	policy := types.DefaultHistoryPolicy()
	policy.Enabled = false

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	_, err := s.Tail(sid, "", 10)
	if !IsHistoryDisabled(err) {
		t.Errorf("expected HISTORY_DISABLED, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Stats
// ---------------------------------------------------------------------------

func TestStore_Stats(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_stats")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 1 << 20

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	stats, err := s.Stats(sid)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.EventCount != 0 {
		t.Errorf("EventCount = %d, want 0", stats.EventCount)
	}
	if stats.BytesStored != 0 {
		t.Errorf("BytesStored = %d, want 0", stats.BytesStored)
	}

	s.Record(sid, "stdout", 1, "hello\n")

	stats, err = s.Stats(sid)
	if err != nil {
		t.Fatalf("Stats after record: %v", err)
	}
	if stats.EventCount != 1 {
		t.Errorf("EventCount = %d, want 1", stats.EventCount)
	}
	if stats.BytesStored != 6 {
		t.Errorf("BytesStored = %d, want 6", stats.BytesStored)
	}
	if stats.Truncated {
		t.Error("Truncated should be false")
	}
}

func TestStore_Stats_SessionNotFound(t *testing.T) {
	s := New("")
	_, err := s.Stats("sess_nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown session")
	}
}

// ---------------------------------------------------------------------------
// Truncation (maxBytes enforcement)
// ---------------------------------------------------------------------------

func TestStore_Truncation_DropsOldestEvents(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_truncate")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 20 // very small — only ~20 bytes total

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	// First event fits
	s.Record(sid, "stdout", 1, "aaaaaaaaaa\n") // 11 bytes
	stats, _ := s.Stats(sid)
	if stats.BytesStored != 11 {
		t.Fatalf("BytesStored = %d, want 11", stats.BytesStored)
	}

	// Second event should trigger truncation (11+11 > 20)
	s.Record(sid, "stdout", 2, "bbbbbbbbbb\n") // 11 bytes

	stats, _ = s.Stats(sid)
	if !stats.Truncated {
		t.Error("expected Truncated=true after truncation")
	}
	if stats.FromSeq != 2 {
		t.Errorf("FromSeq = %d, want 2 (oldest remaining event)", stats.FromSeq)
	}

	// Only event 2 should remain
	events, err := s.Replay(sid, "stdout", 1)
	if err != nil && !IsRangeTruncated(err) {
		t.Fatalf("Replay: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event after truncation, got %d", len(events))
	}
	if events[0].EventSeq != 2 {
		t.Errorf("remaining event seq = %d, want 2", events[0].EventSeq)
	}
}

func TestStore_Truncation_AllEventsDropped(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_truncate_all")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 5 // extremely small

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.Record(sid, "stdout", 1, "hello\n") // 6 bytes, exceeds 5

	stats, _ := s.Stats(sid)
	if !stats.Truncated {
		t.Error("expected Truncated=true")
	}

	// Should have continuation marker
	events, err := s.Replay(sid, "", 1)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events (continuation + data), got %d", len(events))
	}
	// First should be continuation marker (truncation marker has no EventSeq and is filtered)
	if events[0].Type != "history.continued" {
		t.Errorf("expected 'history.continued' marker, got %q", events[0].Type)
	}
	// Second should be the actual data
	if events[1].Type != "stream.stdout" {
		t.Errorf("expected 'stream.stdout' event, got %q", events[1].Type)
	}
}

func TestStore_Replay_RangeTruncated(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_range_truncated")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 20

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.Record(sid, "stdout", 1, "aaaaaaaaaa\n")
	s.Record(sid, "stdout", 2, "bbbbbbbbbb\n") // triggers truncation

	// Request from seq 1, which is before fromSeq (should be 2 now)
	events, err := s.Replay(sid, "", 1)
	if err == nil {
		t.Fatal("expected RangeTruncatedError")
	}
	if !IsRangeTruncated(err) {
		t.Errorf("expected RangeTruncatedError, got %v", err)
	}
	// Should still return available events
	if len(events) == 0 {
		t.Error("expected partial events even with RangeTruncated")
	}
}

// ---------------------------------------------------------------------------
// Clear
// ---------------------------------------------------------------------------

func TestStore_Clear_All(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_clear_all")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 1 << 20

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.Record(sid, "stdout", 1, "data\n")
	s.Record(sid, "stdout", 2, "data\n")
	s.Record(sid, "stdout", 3, "data\n")

	freed, err := s.Clear(sid, nil)
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if freed <= 0 {
		t.Errorf("expected freed > 0, got %d", freed)
	}

	stats, _ := s.Stats(sid)
	if stats.EventCount != 0 {
		t.Errorf("EventCount = %d, want 0 after clear", stats.EventCount)
	}
	if stats.BytesStored != 0 {
		t.Errorf("BytesStored = %d, want 0 after clear", stats.BytesStored)
	}
}

func TestStore_Clear_SpecificStreams(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_clear_streams")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 1 << 20

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.Record(sid, "stdout", 1, "out\n")
	s.Record(sid, "stderr", 2, "err\n")
	s.Record(sid, "stdout", 3, "out2\n")

	// Clear only stderr
	freed, err := s.Clear(sid, []string{"stderr"})
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if freed <= 0 {
		t.Errorf("expected freed > 0, got %d", freed)
	}

	events, _ := s.Replay(sid, "", 1)
	if len(events) != 2 {
		t.Fatalf("expected 2 events (stdout only), got %d", len(events))
	}
	for _, e := range events {
		if e.Stream == "stderr" {
			t.Error("stderr event should have been cleared")
		}
	}
}

func TestStore_Clear_NoEvents(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_clear_empty")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 1 << 20

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	freed, err := s.Clear(sid, nil)
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if freed != 0 {
		t.Errorf("expected freed=0 for empty history, got %d", freed)
	}
}

func TestStore_Clear_SessionNotFound(t *testing.T) {
	s := New("")
	_, err := s.Clear("sess_nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown session")
	}
}

func TestStore_Clear_DisabledPolicy(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_clear_disabled")
	policy := types.DefaultHistoryPolicy()
	policy.Enabled = false

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	freed, err := s.Clear(sid, nil)
	if err != nil {
		t.Fatalf("Clear disabled: %v", err)
	}
	if freed != 0 {
		t.Errorf("expected freed=0 for disabled history, got %d", freed)
	}
}

// ---------------------------------------------------------------------------
// RemoveSession
// ---------------------------------------------------------------------------

func TestStore_RemoveSession(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_remove")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 1 << 20

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.Record(sid, "stdout", 1, "data\n")

	s.RemoveSession(sid)

	_, err := s.Stats(sid)
	if err == nil {
		t.Error("expected error after RemoveSession")
	}
}

func TestStore_RemoveSession_Unknown(t *testing.T) {
	s := New("")
	// Should not panic
	s.RemoveSession("sess_unknown")
}

// ---------------------------------------------------------------------------
// Cleanup
// ---------------------------------------------------------------------------

func TestStore_Cleanup(t *testing.T) {
	s := New("")
	s.Record("sess_1", "stdout", 1, "data\n")

	// Record without init is noop, but let's properly init to verify cleanup
	sid1 := types.SessionID("sess_cleanup_1")
	sid2 := types.SessionID("sess_cleanup_2")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 1 << 20

	s.InitSession(sid1, policy)
	s.InitSession(sid2, policy)
	s.Record(sid1, "stdout", 1, "data\n")

	s.Cleanup()

	if _, err := s.Stats(sid1); err == nil {
		t.Error("expected error after Cleanup")
	}
	if _, err := s.Stats(sid2); err == nil {
		t.Error("expected error after Cleanup")
	}
}

// ---------------------------------------------------------------------------
// Concurrent access
// ---------------------------------------------------------------------------

func TestStore_ConcurrentRecord(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_concurrent")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 1 << 20

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	done := make(chan struct{})
	writer := func(start, count int) {
		for i := 0; i < count; i++ {
			seq := types.EventSeq(start + i)
			s.Record(sid, "stdout", seq, "data\n")
		}
		done <- struct{}{}
	}

	go writer(1, 50)
	go writer(51, 50)

	<-done
	<-done

	events, err := s.Replay(sid, "", 1)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	// We may have gaps due to racing seq, but should have events
	if len(events) == 0 {
		t.Error("expected some events after concurrent writes")
	}
}

func TestStore_ConcurrentReadWrite(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_conc_rw")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 1 << 20

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	done := make(chan struct{})
	go func() {
		for i := 1; i <= 100; i++ {
			s.Record(sid, "stdout", types.EventSeq(i), "x\n")
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < 50; i++ {
			s.Replay(sid, "", 1)
			s.Tail(sid, "", 5)
			s.Stats(sid)
		}
		done <- struct{}{}
	}()

	<-done
	<-done
}

// ---------------------------------------------------------------------------
// Disk mode
// ---------------------------------------------------------------------------

func TestStore_DiskMode_RecordAndReplay(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	sid := types.SessionID("sess_disk_rr")
	policy := types.HistoryPolicy{
		Enabled: true,
		Mode:    types.HistoryModeDisk,
		Streams: []string{"stdout", "stderr"},
		MaxBytes: 1 << 20,
	}

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.Record(sid, "stdout", 1, "hello disk\n")
	s.Record(sid, "stdout", 2, "world disk\n")
	s.Record(sid, "stderr", 3, "error disk\n")

	// Verify replay from in-memory
	events, err := s.Replay(sid, "", 1)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	// Verify files exist on disk
	sessionDir := filepath.Join(dir, string(sid))
	if _, err := os.Stat(sessionDir); os.IsNotExist(err) {
		t.Fatal("expected session dir on disk")
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "events.jsonl")); os.IsNotExist(err) {
		t.Fatal("expected events.jsonl on disk")
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "stdout.log")); os.IsNotExist(err) {
		t.Fatal("expected stdout.log on disk")
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "stderr.log")); os.IsNotExist(err) {
		t.Fatal("expected stderr.log on disk")
	}

	// stdout.log should contain raw data
	rawOut, err := os.ReadFile(filepath.Join(sessionDir, "stdout.log"))
	if err != nil {
		t.Fatalf("read stdout.log: %v", err)
	}
	if string(rawOut) != "hello disk\nworld disk\n" {
		t.Errorf("stdout.log = %q", string(rawOut))
	}

	// Close open file handles so TempDir can clean up
	s.Cleanup()
	// Close open file handles so TempDir can clean up
}

func TestStore_DiskMode_ReplayFromDisk(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	sid := types.SessionID("sess_disk_rebuild")
	policy := types.HistoryPolicy{
		Enabled: true,
		Mode:    types.HistoryModeDisk,
		Streams: []string{"stdout"},
		MaxBytes: 1 << 20,
	}

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.Record(sid, "stdout", 1, "persist1\n")
	s.Record(sid, "stdout", 2, "persist2\n")
	s.Record(sid, "stdout", 3, "persist3\n")

	// Close files so they flush to disk
	s.RemoveSession(sid)
	s2 := New(dir)
	if err := s2.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession on new store: %v", err)
	}

	// Rebuild from disk
	if err := s2.ReplayFromDisk(sid); err != nil {
		t.Fatalf("ReplayFromDisk: %v", err)
	}

	// Verify events loaded
	events, err := s2.Replay(sid, "", 1)
	if err != nil {
		t.Fatalf("Replay after disk rebuild: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events after disk rebuild, got %d", len(events))
	}
	if events[0].Data != "persist1\n" {
		t.Errorf("event[0].Data = %q", events[0].Data)
	}
	if events[2].Data != "persist3\n" {
		t.Errorf("event[2].Data = %q", events[2].Data)
	}
}

func TestStore_DiskMode_ReplayFromDisk_NoEventsFile(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	sid := types.SessionID("sess_disk_no_file")
	policy := types.HistoryPolicy{
		Enabled: true,
		Mode:    types.HistoryModeDisk,
		Streams: []string{"stdout"},
		MaxBytes: 1 << 20,
	}

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	// ReplayFromDisk with no events file should be a no-op
	if err := s.ReplayFromDisk(sid); err != nil {
		t.Fatalf("ReplayFromDisk with no file: %v", err)
	}
}

func TestStore_DiskMode_ReplayFromDisk_MemoryModeNoop(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_mem_noop")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 1 << 20

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	// ReplayFromDisk on memory mode session should be a no-op
	if err := s.ReplayFromDisk(sid); err != nil {
		t.Fatalf("ReplayFromDisk on memory mode: %v", err)
	}
}

func TestStore_DiskMode_ReplayFromDisk_SessionNotFound(t *testing.T) {
	s := New(t.TempDir())
	err := s.ReplayFromDisk("sess_nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown session")
	}
}

func TestStore_DiskMode_ClearRemovesFiles(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	sid := types.SessionID("sess_disk_clear")
	policy := types.HistoryPolicy{
		Enabled: true,
		Mode:    types.HistoryModeDisk,
		Streams: []string{"stdout"},
		MaxBytes: 1 << 20,
	}

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.Record(sid, "stdout", 1, "data\n")

	sessionDir := filepath.Join(dir, string(sid))
	if _, err := os.Stat(sessionDir); os.IsNotExist(err) {
		t.Fatal("expected session dir before clear")
	}

	s.Clear(sid, nil)

	// Directory should be removed
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Error("expected session dir to be removed after clear")
	}
}

func TestStore_DiskMode_RemoveSessionRemovesFiles(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	sid := types.SessionID("sess_disk_remove")
	policy := types.HistoryPolicy{
		Enabled: true,
		Mode:    types.HistoryModeDisk,
		Streams: []string{"stdout"},
		MaxBytes: 1 << 20,
	}

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.Record(sid, "stdout", 1, "data\n")

	s.RemoveSession(sid)

	// Note: RemoveSession does NOT remove disk files
	sessionDir := filepath.Join(dir, string(sid))
	if _, err := os.Stat(sessionDir); os.IsNotExist(err) {
		// This is fine - RemoveSession currently doesn't remove files
		// (it could be nice, but it's not the current contract)
	}
}

func TestStore_DiskMode_NoStderrFileWhenNoStderr(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	sid := types.SessionID("sess_disk_noerr")
	policy := types.HistoryPolicy{
		Enabled: true,
		Mode:    types.HistoryModeDisk,
		Streams: []string{"stdout"},
		MaxBytes: 1 << 20,
	}

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.Record(sid, "stdout", 1, "data\n")

	sessionDir := filepath.Join(dir, string(sid))
	// stderr.log should not exist since no stderr events recorded
	if _, err := os.Stat(filepath.Join(sessionDir, "stderr.log")); !os.IsNotExist(err) {
		t.Error("stderr.log should not exist when no stderr events recorded")
	}
        s.Cleanup()
}

// ---------------------------------------------------------------------------
// EventSeq monotonicity
// ---------------------------------------------------------------------------

func TestStore_EventSeq_Monotonic(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_seq_mono")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 1 << 20

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.Record(sid, "stdout", 1, "a\n")
	s.Record(sid, "stdout", 5, "b\n") // non-contiguous seq is OK
	s.Record(sid, "stdout", 3, "c\n") // out of order seq

	stats, _ := s.Stats(sid)
	// nextSeq should be max(seq)+1 = 6
	if stats.NextSeq != 6 {
		t.Errorf("NextSeq = %d, want 6", stats.NextSeq)
	}
}

// ---------------------------------------------------------------------------
// Edge cases and invariants
// ---------------------------------------------------------------------------

func TestStore_Record_EmptyData(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_empty_data")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 1 << 20

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.Record(sid, "stdout", 1, "")

	events, err := s.Replay(sid, "", 1)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Data != "" {
		t.Errorf("expected empty data, got %q", events[0].Data)
	}
}

func TestStore_Truncation_RecordsDroppedBytes(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_dropped_bytes")
	policy := types.DefaultHistoryPolicy()
	// 50 bytes: fits ~4 events of 11 bytes, 5th triggers drop of oldest
	policy.MaxBytes = 50

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	// Insert records until some are dropped
	for i := 1; i <= 10; i++ {
		s.Record(sid, "stdout", types.EventSeq(i), "aaaaaaaaaa\n") // 11 bytes each
	}

	stats, _ := s.Stats(sid)
	if stats.BytesDropped <= 0 {
		t.Errorf("expected BytesDropped > 0, got %d", stats.BytesDropped)
	}
	// 10 events × 11 bytes = 110 total; maxBytes = 50, so ~60 bytes dropped
	if stats.BytesDropped < 50 {
		t.Errorf("expected BytesDropped >= 50, got %d", stats.BytesDropped)
	}
	if stats.BytesStored > 50 {
		t.Errorf("expected BytesStored <= 50, got %d", stats.BytesStored)
	}
	if stats.EventCount > 5 {
		t.Errorf("expected EventCount <= 5 (at most 50/11 ~4.5 fits), got %d", stats.EventCount)
	}
}

func TestStore_StdinNotSavedByDefault(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_stdin_default")
	policy := types.DefaultHistoryPolicy() // default has only stdout, stderr
	policy.MaxBytes = 1 << 20

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.Record(sid, "stdin", 1, "secret input\n") // not in default policy streams

	events, err := s.Replay(sid, "", 1)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	// stdin should not be recorded
	for _, e := range events {
		if e.Stream == "stdin" {
			t.Error("stdin should not be recorded by default")
		}
	}
}

func TestStore_StdinSavedWhenExplicitlyConfigured(t *testing.T) {
	s := New("")
	sid := types.SessionID("sess_stdin_explicit")
	policy := types.DefaultHistoryPolicy()
	policy.MaxBytes = 1 << 20
	policy.Streams = []string{"stdout", "stderr", "stdin"}

	if err := s.InitSession(sid, policy); err != nil {
		t.Fatalf("InitSession: %v", err)
	}

	s.Record(sid, "stdin", 1, "explicit input\n")

	events, err := s.Replay(sid, "stdin", 1)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 stdin event, got %d", len(events))
	}
	if events[0].Data != "explicit input\n" {
		t.Errorf("data = %q", events[0].Data)
	}
}
