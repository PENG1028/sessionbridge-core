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

// ---------------------------------------------------------------------------
// Session state machine
// ---------------------------------------------------------------------------

func TestSession_InitialState(t *testing.T) {
	sess := NewSession("sess_1", "test", "bash", "/tmp", 1000)
	if sess.State != StateCreated {
		t.Errorf("initial state = %q, want %q", sess.State, StateCreated)
	}
}

func TestSession_TransitionCreatedToRunning(t *testing.T) {
	sess := NewSession("sess_1", "test", "bash", "/tmp", 1000)
	if err := sess.TransitionState(StateRunning, 1001); err != nil {
		t.Fatalf("transition failed: %v", err)
	}
	if sess.State != StateRunning {
		t.Errorf("state = %q, want %q", sess.State, StateRunning)
	}
	if sess.UpdatedAt != 1001 {
		t.Errorf("UpdatedAt = %d, want 1001", sess.UpdatedAt)
	}
}

func TestSession_TransitionRunningToExited(t *testing.T) {
	sess := NewSession("sess_1", "test", "bash", "/tmp", 1000)
	sess.TransitionState(StateRunning, 1001)
	if err := sess.TransitionState(StateExited, 1002); err != nil {
		t.Fatalf("transition to exited failed: %v", err)
	}
	if sess.State != StateExited {
		t.Errorf("state = %q, want %q", sess.State, StateExited)
	}
}

func TestSession_TransitionRunningToInterrupted(t *testing.T) {
	sess := NewSession("sess_1", "test", "bash", "/tmp", 1000)
	sess.TransitionState(StateRunning, 1001)
	if err := sess.Interrupt(1002); err != nil {
		t.Fatalf("Interrupt failed: %v", err)
	}
	if sess.State != StateInterrupted {
		t.Errorf("state = %q, want %q", sess.State, StateInterrupted)
	}
}

func TestSession_TransitionInterruptedToResumable(t *testing.T) {
	sess := NewSession("sess_1", "test", "bash", "/tmp", 1000)
	sess.TransitionState(StateRunning, 1001)
	sess.Interrupt(1002)
	if err := sess.MakeResumable(1003); err != nil {
		t.Fatalf("MakeResumable failed: %v", err)
	}
	if sess.State != StateResumable {
		t.Errorf("state = %q, want %q", sess.State, StateResumable)
	}
	if !sess.IsResumable() {
		t.Error("IsResumable should be true")
	}
}

func TestSession_TransitionResumableToRunning(t *testing.T) {
	sess := NewSession("sess_1", "test", "bash", "/tmp", 1000)
	sess.TransitionState(StateRunning, 1001)
	sess.Interrupt(1002)
	sess.MakeResumable(1003)
	if err := sess.TransitionState(StateRunning, 1004); err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if sess.State != StateRunning {
		t.Errorf("state = %q, want %q", sess.State, StateRunning)
	}
}

func TestSession_InvalidTransition(t *testing.T) {
	tests := []struct {
		name     string
		from     string
		to       string
		setup    func(*Session)
	}{
		{"created to exited", StateCreated, StateExited, nil},
		{"created to interrupted", StateCreated, StateInterrupted, nil},
		{"created to resumable", StateCreated, StateResumable, nil},
		{"exited to running", StateExited, StateRunning, func(s *Session) { s.State = StateExited }},
		{"error to running", StateError, StateRunning, func(s *Session) { s.State = StateError }},
		{"closed to anything", StateClosed, StateRunning, func(s *Session) { s.State = StateClosed }},
		{"resumable to created", StateResumable, StateCreated, func(s *Session) { s.State = StateResumable }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess := NewSession("sess_1", "test", "bash", "/tmp", 1000)
			if tt.setup != nil {
				tt.setup(sess)
			}
			if err := sess.TransitionState(tt.to, 2000); err == nil {
				t.Errorf("expected error for transition %s → %s", tt.from, tt.to)
			}
		})
	}
}

func TestSession_CloseFromAnyNonTerminal(t *testing.T) {
	states := []string{StateCreated, StateRunning, StateInterrupted, StateResumable, StateExited, StateError}
	for _, state := range states {
		t.Run("close_from_"+state, func(t *testing.T) {
			sess := NewSession("sess_1", "test", "bash", "/tmp", 1000)
			sess.State = state
			err := sess.Close(2000)
			if state == StateClosed {
				// Already closed — Close should fail since closed → closed is invalid
				if err == nil {
					t.Error("expected error when closing already-closed session")
				}
			} else if err != nil {
				t.Errorf("Close from %s failed: %v", state, err)
			}
		})
	}
}

func TestSession_IsTerminal(t *testing.T) {
	tests := []struct {
		state string
		want  bool
	}{
		{StateCreated, false},
		{StateRunning, false},
		{StateInterrupted, false},
		{StateResumable, false},
		{StateExited, true},
		{StateError, true},
		{StateClosed, true},
	}
	for _, tt := range tests {
		sess := NewSession("sess_1", "test", "bash", "/tmp", 1000)
		sess.State = tt.state
		if got := sess.IsTerminal(); got != tt.want {
			t.Errorf("IsTerminal(%s) = %v, want %v", tt.state, got, tt.want)
		}
	}
}

func TestSession_CanStream(t *testing.T) {
	tests := []struct {
		state string
		want  bool
	}{
		{StateCreated, true},
		{StateRunning, true},
		{StateInterrupted, false},
		{StateResumable, false},
		{StateExited, false},
		{StateError, false},
		{StateClosed, false},
	}
	for _, tt := range tests {
		sess := NewSession("sess_1", "test", "bash", "/tmp", 1000)
		sess.State = tt.state
		if got := sess.CanStream(); got != tt.want {
			t.Errorf("CanStream(%s) = %v, want %v", tt.state, got, tt.want)
		}
	}
}

func TestStore_Cleanup(t *testing.T) {
	s := NewStore()
	s.Create("p1", "", "", 0)
	s.Create("p2", "", "", 0)
	s.Cleanup()
	if s.Count() != 0 {
		t.Errorf("expected 0 after cleanup, got %d", s.Count())
	}
}

func TestSession_FullLifecycle(t *testing.T) {
	sess := NewSession("sess_lifecycle", "test", "bash", "/tmp", 1000)

	// created → running
	if err := sess.TransitionState(StateRunning, 1100); err != nil {
		t.Fatalf("created→running: %v", err)
	}
	if !sess.CanStream() {
		t.Error("should be able to stream in running state")
	}

	// running → interrupted
	if err := sess.Interrupt(1200); err != nil {
		t.Fatalf("running→interrupted: %v", err)
	}
	if sess.CanStream() {
		t.Error("should NOT be able to stream in interrupted state")
	}
	if !sess.IsResumable() {
		t.Error("interrupted session should be IsResumable")
	}

	// interrupted → resumable
	if err := sess.MakeResumable(1300); err != nil {
		t.Fatalf("interrupted→resumable: %v", err)
	}
	if !sess.IsResumable() {
		t.Error("resumable session should be IsResumable")
	}

	// resumable → running (reconnect)
	if err := sess.TransitionState(StateRunning, 1400); err != nil {
		t.Fatalf("resumable→running: %v", err)
	}
	if !sess.CanStream() {
		t.Error("should be able to stream after resume")
	}

	// running → exited
	if err := sess.TransitionState(StateExited, 1500); err != nil {
		t.Fatalf("running→exited: %v", err)
	}
	if !sess.IsTerminal() {
		t.Error("exited session should be terminal")
	}

	// exited → closed
	if err := sess.Close(1600); err != nil {
		t.Fatalf("exited→closed: %v", err)
	}
	if !sess.IsTerminal() {
		t.Error("closed session should be terminal")
	}
}
