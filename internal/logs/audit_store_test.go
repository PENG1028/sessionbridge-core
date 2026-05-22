package logs

import (
	"testing"
	"time"
)

func TestAuditStore_RecordListReturnsCopy(t *testing.T) {
	s := NewAuditStore()
	r := AuditRecord{
		Timestamp: time.Now().UnixMilli(),
		EventType: "capability.call",
		Actor:     "web:user1",
		Target:    "core/system.info",
		Outcome:   "ok",
	}
	s.Record(r)

	entries := s.List("", "", "", 10)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].EventType != "capability.call" {
		t.Errorf("unexpected eventType: %s", entries[0].EventType)
	}

	// Verify copy: mutate returned slice
	entries[0].Actor = "mutated"
	entries2 := s.List("", "", "", 10)
	if entries2[0].Actor != "web:user1" {
		t.Errorf("List did not return a copy: got %q", entries2[0].Actor)
	}
}

func TestAuditStore_ListRecentFirst(t *testing.T) {
	s := NewAuditStore()
	s.Record(AuditRecord{EventType: "first", Actor: "a", Target: "t", Outcome: "ok"})
	s.Record(AuditRecord{EventType: "second", Actor: "a", Target: "t", Outcome: "ok"})

	entries := s.List("", "", "", 10)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// Most recent first
	if entries[0].EventType != "second" {
		t.Errorf("expected most recent=second, got %s", entries[0].EventType)
	}
	if entries[1].EventType != "first" {
		t.Errorf("expected first, got %s", entries[1].EventType)
	}
}

func TestAuditStore_ListEventTypeFilter(t *testing.T) {
	s := NewAuditStore()
	s.Record(AuditRecord{EventType: "capability.call", Actor: "a", Target: "t", Outcome: "ok"})
	s.Record(AuditRecord{EventType: "permission.grant", Actor: "a", Target: "t", Outcome: "ok"})

	entries := s.List("permission.grant", "", "", 10)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].EventType != "permission.grant" {
		t.Errorf("unexpected eventType: %s", entries[0].EventType)
	}
}

func TestAuditStore_ListActorFilter(t *testing.T) {
	s := NewAuditStore()
	s.Record(AuditRecord{EventType: "e", Actor: "web:alice", Target: "t", Outcome: "ok"})
	s.Record(AuditRecord{EventType: "e", Actor: "web:bob", Target: "t", Outcome: "ok"})

	entries := s.List("", "web:alice", "", 10)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry for alice, got %d", len(entries))
	}
}

func TestAuditStore_ListTargetFilter(t *testing.T) {
	s := NewAuditStore()
	s.Record(AuditRecord{EventType: "e", Actor: "a", Target: "core/system.info", Outcome: "ok"})
	s.Record(AuditRecord{EventType: "e", Actor: "a", Target: "plugin/run.create", Outcome: "ok"})

	entries := s.List("", "", "plugin/run.create", 10)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry for target, got %d", len(entries))
	}
}

func TestAuditStore_ListLimitClamp(t *testing.T) {
	s := NewAuditStore()
	for i := 0; i < 5; i++ {
		s.Record(AuditRecord{EventType: "e", Actor: "a", Target: "t", Outcome: "ok"})
	}

	// limit <= 0 defaults to 100
	entries := s.List("", "", "", 0)
	if len(entries) != 5 {
		t.Errorf("limit=0 defaults to 100, got %d", len(entries))
	}

	// limit > 1000 clamped
	entries = s.List("", "", "", 2000)
	if len(entries) != 5 {
		t.Errorf("limit=2000 clamped to 1000, got %d", len(entries))
	}

	// exact limit
	entries = s.List("", "", "", 2)
	if len(entries) != 2 {
		t.Errorf("limit=2 should return 2, got %d", len(entries))
	}
}

func TestAuditStore_ListEmptyFilters(t *testing.T) {
	s := NewAuditStore()
	entries := s.List("", "", "", 10)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestAuditStore_AuditIDGenerated(t *testing.T) {
	s := NewAuditStore()
	r := AuditRecord{
		EventType: "test",
		Actor:     "a",
		Target:    "t",
		Outcome:   "ok",
	}
	s.Record(r)

	entries := s.List("", "", "", 1)
	if entries[0].AuditID == "" {
		t.Error("expected AuditID to be auto-generated")
	}
}

func TestAuditStore_Concurrent(t *testing.T) {
	s := NewAuditStore()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			s.Record(AuditRecord{EventType: "e", Actor: "a", Target: "t", Outcome: "ok"})
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < 50; i++ {
			s.List("", "", "", 20)
		}
		done <- struct{}{}
	}()
	<-done
	<-done
}
