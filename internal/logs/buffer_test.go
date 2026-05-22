package logs

import (
	"fmt"
	"testing"
	"time"
)

func TestBuffer_AddTailReturnsCopy(t *testing.T) {
	b := NewBuffer(100)

	e := Entry{
		Timestamp: time.Now().UnixMilli(),
		Level:     LevelInfo,
		Source:    "core",
		Message:   "test message",
	}
	b.Add(e)

	entries := b.Tail("", "", 10)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Message != "test message" {
		t.Errorf("unexpected message: %s", entries[0].Message)
	}

	// Verify copy: mutating returned slice does not affect internal state.
	entries[0].Message = "mutated"
	entries2 := b.Tail("", "", 10)
	if entries2[0].Message != "test message" {
		t.Errorf("Tail did not return a copy: got %q, expected %q", entries2[0].Message, "test message")
	}
}

func TestBuffer_AddOverCapacityDropsOldest(t *testing.T) {
	b := NewBuffer(3)

	for i := 0; i < 5; i++ {
		b.Add(Entry{
			Timestamp: int64(i),
			Level:     LevelInfo,
			Source:    "core",
			Message:   fmt.Sprintf("msg-%d", i),
		})
	}

	entries := b.Tail("", "", 10)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries (capacity), got %d", len(entries))
	}
	// Oldest should be msg-2 (index 2 was overwritten by 5 entries in ring of 3)
	if entries[0].Message != "msg-2" {
		t.Errorf("expected oldest=msg-2, got %s", entries[0].Message)
	}
	if entries[1].Message != "msg-3" {
		t.Errorf("expected msg-3, got %s", entries[1].Message)
	}
	if entries[2].Message != "msg-4" {
		t.Errorf("expected msg-4, got %s", entries[2].Message)
	}
}

func TestBuffer_TailLimitClamp(t *testing.T) {
	b := NewBuffer(10)
	for i := 0; i < 5; i++ {
		b.Add(Entry{Level: LevelInfo, Source: "core", Message: "x"})
	}

	// limit <= 0 defaults to 100
	entries := b.Tail("", "", 0)
	if len(entries) != 5 {
		t.Errorf("limit=0 should default to 100, got %d entries", len(entries))
	}

	// limit > 1000 clamped
	entries = b.Tail("", "", 2000)
	if len(entries) != 5 {
		t.Errorf("limit=2000 clamped to 1000, got %d entries", len(entries))
	}

	// exact limit
	entries = b.Tail("", "", 2)
	if len(entries) != 2 {
		t.Errorf("limit=2 should return 2, got %d", len(entries))
	}
}

func TestBuffer_TailLevelFilter(t *testing.T) {
	b := NewBuffer(10)
	b.Add(Entry{Level: LevelInfo, Source: "core", Message: "info-msg"})
	b.Add(Entry{Level: LevelError, Source: "core", Message: "error-msg"})
	b.Add(Entry{Level: LevelWarn, Source: "core", Message: "warn-msg"})

	entries := b.Tail("", LevelError, 10)
	if len(entries) != 1 {
		t.Fatalf("expected 1 error entry, got %d", len(entries))
	}
	if entries[0].Message != "error-msg" {
		t.Errorf("unexpected message: %s", entries[0].Message)
	}
}

func TestBuffer_TailSourceFilter(t *testing.T) {
	b := NewBuffer(10)
	b.Add(Entry{Level: LevelInfo, Source: "core", Message: "core-msg"})
	b.Add(Entry{Level: LevelInfo, Source: "plugin", Message: "plugin-msg"})

	entries := b.Tail("plugin", "", 10)
	if len(entries) != 1 {
		t.Fatalf("expected 1 plugin entry, got %d", len(entries))
	}
	if entries[0].Message != "plugin-msg" {
		t.Errorf("unexpected message: %s", entries[0].Message)
	}
}

func TestBuffer_TailEmpty(t *testing.T) {
	b := NewBuffer(10)
	entries := b.Tail("", "", 10)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestBuffer_QueryFilters(t *testing.T) {
	b := NewBuffer(20)
	b.Add(Entry{Level: LevelInfo, Source: "core", PluginID: "p1", Message: "p1-info"})
	b.Add(Entry{Level: LevelError, Source: "plugin", PluginID: "p1", Message: "p1-error"})
	b.Add(Entry{Level: LevelInfo, Source: "plugin", PluginID: "p2", Message: "p2-info"})

	// Filter by pluginID
	entries := b.Query("", "p1", "", 10)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries for p1, got %d", len(entries))
	}

	// Filter by pluginID + level
	entries = b.Query("", "p1", LevelError, 10)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry for p1+error, got %d", len(entries))
	}
	if entries[0].Message != "p1-error" {
		t.Errorf("unexpected message: %s", entries[0].Message)
	}

	// Filter by source + pluginID
	entries = b.Query("core", "p1", "", 10)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry for core+p1, got %d", len(entries))
	}
}

func TestBuffer_QueryLimit(t *testing.T) {
	b := NewBuffer(20)
	for i := 0; i < 10; i++ {
		b.Add(Entry{Level: LevelInfo, Source: "core", Message: fmt.Sprintf("msg-%d", i)})
	}

	entries := b.Query("", "", "", 3)
	if len(entries) != 3 {
		t.Errorf("expected 3 entries, got %d", len(entries))
	}
}

func TestBuffer_DefaultCapacity(t *testing.T) {
	b := NewBuffer(0)
	if b.capacity != 1000 {
		t.Errorf("expected default capacity 1000, got %d", b.capacity)
	}
}

func TestBuffer_Concurrent(t *testing.T) {
	b := NewBuffer(500)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			b.Add(Entry{Level: LevelInfo, Source: "core", Message: fmt.Sprintf("msg-%d", i)})
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < 100; i++ {
			b.Tail("", "", 50)
		}
		done <- struct{}{}
	}()
	<-done
	<-done
}
