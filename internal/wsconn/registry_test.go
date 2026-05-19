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
