package session

import (
	"testing"

	"github.com/user/sessionnode/go-core/pkg/types"
)

func TestStore_CreateAndGet(t *testing.T) {
	s := NewStore()
	id := s.Create("test-plugin", "bash", "/home", 1000)

	got := s.Get(id)
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if got.ID != id {
		t.Errorf("ID = %q, want %q", got.ID, id)
	}
	if got.PluginID != "test-plugin" {
		t.Errorf("PluginID = %q", got.PluginID)
	}
	if got.State != StateCreated {
		t.Errorf("State = %q, want %q", got.State, StateCreated)
	}
	if got.Command != "bash" {
		t.Errorf("Command = %q", got.Command)
	}
}

func TestStore_Get_NotFound(t *testing.T) {
	s := NewStore()
	got := s.Get("sess_nonexistent")
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestStore_Destroy(t *testing.T) {
	s := NewStore()
	id := s.Create("test", "", "", 0)
	s.Destroy(id)

	if got := s.Get(id); got != nil {
		t.Error("session should be nil after destroy")
	}
}

func TestStore_List(t *testing.T) {
	s := NewStore()
	if len(s.List()) != 0 {
		t.Error("new store should have 0 sessions")
	}

	s.Create("p1", "", "", 0)
	s.Create("p2", "", "", 0)
	s.Create("p3", "", "", 0)

	sessions := s.List()
	if len(sessions) != 3 {
		t.Errorf("expected 3 sessions, got %d", len(sessions))
	}
}

func TestStore_Count(t *testing.T) {
	s := NewStore()
	if s.Count() != 0 {
		t.Error("new store count should be 0")
	}

	s.Create("p", "", "", 0)
	if s.Count() != 1 {
		t.Errorf("count = %d, want 1", s.Count())
	}
}

func TestStore_ConcurrentAccess(t *testing.T) {
	s := NewStore()
	done := make(chan struct{})

	// Concurrent creates
	go func() {
		for i := 0; i < 50; i++ {
			s.Create("p", "", "", 0)
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < 50; i++ {
			s.Create("p", "", "", 0)
		}
		done <- struct{}{}
	}()

	<-done
	<-done

	if s.Count() != 100 {
		t.Errorf("expected 100 sessions, got %d", s.Count())
	}
}

func TestSession_NewSession(t *testing.T) {
	sess := NewSession("sess_1", "test-plugin", "echo hi", "/tmp", 1000)
	if sess.ID != types.SessionID("sess_1") {
		t.Errorf("ID = %q", sess.ID)
	}
	if sess.Streams == nil {
		t.Fatal("Streams should not be nil")
	}
	if _, ok := sess.Streams["stdout"]; !ok {
		t.Error("missing stdout stream")
	}
	if _, ok := sess.Streams["stderr"]; !ok {
		t.Error("missing stderr stream")
	}
	if _, ok := sess.Streams["stdin"]; !ok {
		t.Error("missing stdin stream")
	}
}

func TestStream_WriteAndRead(t *testing.T) {
	stream := NewStream("stdout", 10)
	stream.Write([]byte("hello"))
	stream.Write([]byte("world"))
	if got := string(stream.Read()); got != "helloworld" {
		t.Errorf("Read = %q, want %q", got, "helloworld")
	}
}

func TestStream_RollingBuffer(t *testing.T) {
	stream := NewStream("stdout", 10)
	stream.Write([]byte("abcdefghijklmno"))
	if got := string(stream.Read()); got != "fghijklmno" {
		t.Errorf("rolling buffer = %q, want %q", got, "fghijklmno")
	}
}

func TestStream_WriteEmpty(t *testing.T) {
	stream := NewStream("stdout", 10)
	stream.Write(nil)
	stream.Write([]byte{})
	if len(stream.Read()) != 0 {
		t.Error("empty writes should not change buffer")
	}
}

func TestStream_NewStreamDefaultSize(t *testing.T) {
	s := NewStream("stdout", 0)
	if s.MaxSize != 64*1024 {
		t.Errorf("default MaxSize = %d, want %d", s.MaxSize, 64*1024)
	}
}
