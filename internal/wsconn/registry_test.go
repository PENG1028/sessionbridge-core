package wsconn

import (
	"testing"

	"github.com/user/sessionnode/go-core/pkg/types"
)

func TestRegistry_RegisterAndPush(t *testing.T) {
	r := NewRegistry()
	sid := types.SessionID("sess_test")

	ch := make(chan []byte, 10)
	r.Register(sid, ch)

	r.Push(sid, nil) // nil msg → no panic

	select {
	case <-ch:
		// expected - message was pushed
	default:
		t.Error("expected message on channel after Push")
	}
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry()
	sid := types.SessionID("sess_test")

	ch := make(chan []byte, 1)
	r.Register(sid, ch)

	select {
	case ch <- []byte("test"):
	default:
	}

	r.RemoveAllForCh(ch)

	// After remove, push should be no-op (no panic)
	r.Push(sid, nil)
}

func TestRegistry_NoConn(t *testing.T) {
	r := NewRegistry()
	sid := types.SessionID("sess_test")

	// Push with no registered conn
	r.Push(sid, nil) // should not panic
}

func TestRegistry_RemoveAllForCh_ReRegisterPreservesNew(t *testing.T) {
	// Regression test for WS reconnect race:
	// 1. sid registered on ch1
	// 2. sid re-registered on ch2 (new WS connection)
	// 3. RemoveAllForCh(ch1) — old WS cleanup
	// 4. Push(sid) → must route to ch2 only
	r := NewRegistry()
	sid := types.SessionID("sess_reconnect")

	ch1 := make(chan []byte, 10)
	ch2 := make(chan []byte, 10)

	r.Register(sid, ch1)    // initial registration (old WS)
	r.Register(sid, ch2)    // reconnect registration (new WS)
	r.RemoveAllForCh(ch1)   // old WS cleanup

	// Push and verify only ch2 receives
	r.Push(sid, nil)

	// ch2 must have the message
	select {
	case <-ch2:
		// expected
	default:
		t.Error("expected message on ch2 after Push, but got none")
	}

	// ch1 must NOT have the message
	select {
	case <-ch1:
		t.Error("ch1 received message after RemoveAllForCh — old cleanup leaked")
	default:
		// expected — ch1 was cleaned up
	}
}

func TestRegistry_RemoveAllForCh_DoesNotAffectOtherSessions(t *testing.T) {
	// Sessions registered on ch2 should survive RemoveAllForCh(ch1)
	r := NewRegistry()
	sid1 := types.SessionID("sess_a")
	sid2 := types.SessionID("sess_b")

	ch1 := make(chan []byte, 10)
	ch2 := make(chan []byte, 10)

	r.Register(sid1, ch1)  // sid1 → ch1
	r.Register(sid2, ch2)  // sid2 → ch2
	r.RemoveAllForCh(ch1)  // remove all ch1 routes

	r.Push(sid2, nil)      // should still reach ch2

	select {
	case <-ch2:
		// expected — ch2 not affected
	default:
		t.Error("expected message on ch2 — RemoveAllForCh(ch1) should not affect ch2")
	}
}

func TestRegistry_PushChunk(t *testing.T) {
	r := NewRegistry()
	sid := types.SessionID("sess_test")

	ch := make(chan []byte, 10)
	r.Register(sid, ch)

	r.PushChunk(sid, "stdout", 1, "hello")

	select {
	case data := <-ch:
		if len(data) == 0 {
			t.Error("expected non-empty chunk message")
		}
	default:
		t.Error("expected message on channel")
	}
}
