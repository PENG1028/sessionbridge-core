package wsconn

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/user/sessionnode/go-core/pkg/protocol"
	"github.com/user/sessionnode/go-core/pkg/types"
)

func connActor() types.Actor {
	return types.Actor{Type: "web", ID: "tester"}
}

func waitForCh(t *testing.T, ch <-chan []byte, timeout time.Duration) []byte {
	t.Helper()
	select {
	case data := <-ch:
		return data
	case <-time.After(timeout):
		t.Fatal("timeout waiting for channel message")
		return nil
	}
}

func assertNoMsg(t *testing.T, ch <-chan []byte, timeout time.Duration) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("unexpected message on channel")
	case <-time.After(timeout):
	}
}

// msgType extracts the "type" field from a serialized WS message.
func msgType(t *testing.T, data []byte) string {
	t.Helper()
	var m struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m.Type
}

// ─── RegisterConn / UnregisterConn ──────────────────────────────────

func TestRegistry_RegisterConn(t *testing.T) {
	r := NewRegistry()
	ch := make(chan []byte, 10)
	c := r.RegisterConn(ch, connActor())
	if c.ID == "" {
		t.Fatal("expected non-empty connection ID")
	}
	if r.ConnectionCount() != 1 {
		t.Errorf("count = %d, want 1", r.ConnectionCount())
	}
}

func TestRegistry_UnregisterConnCleansSubs(t *testing.T) {
	r := NewRegistry()
	ch1 := make(chan []byte, 10)
	ch2 := make(chan []byte, 10)
	c1 := r.RegisterConn(ch1, connActor())
	c2 := r.RegisterConn(ch2, connActor())
	sid := types.SessionID("sess_test")

	r.Subscribe(c1.ID, sid, []string{"stdout"}, "test", connActor(), 0)
	r.Subscribe(c2.ID, sid, []string{"stdout"}, "test", connActor(), 0)

	if r.SubscriberCount(sid) != 2 {
		t.Fatalf("expected 2 subscribers, got %d", r.SubscriberCount(sid))
	}

	// Disconnect c1
	r.UnregisterConn(c1.ID)

	if r.SubscriberCount(sid) != 1 {
		t.Errorf("expected 1 subscriber after c1 disconnect, got %d", r.SubscriberCount(sid))
	}
	if r.ConnectionCount() != 1 {
		t.Errorf("connection count = %d, want 1", r.ConnectionCount())
	}

	// c2 still receives
	r.PushChunk(sid, "stdout", 1, "hello")
	waitForCh(t, ch2, time.Second)

	// c1 must NOT receive
	assertNoMsg(t, ch1, 200*time.Millisecond)
}

// ─── Subscribe / Unsubscribe ───────────────────────────────────────

func TestRegistry_Subscribe(t *testing.T) {
	r := NewRegistry()
	ch := make(chan []byte, 10)
	c := r.RegisterConn(ch, connActor())
	sid := types.SessionID("sess_test")

	sub := r.Subscribe(c.ID, sid, []string{"stdout", "stderr"}, "test", connActor(), 0)
	if sub.ID == "" {
		t.Fatal("expected non-empty subscription ID")
	}
	if sub.SessionID != sid {
		t.Errorf("sessionId = %s, want %s", sub.SessionID, sid)
	}
	if r.SubscriberCount(sid) != 1 {
		t.Errorf("count = %d, want 1", r.SubscriberCount(sid))
	}
}

func TestRegistry_Unsubscribe(t *testing.T) {
	r := NewRegistry()
	ch := make(chan []byte, 10)
	c := r.RegisterConn(ch, connActor())
	sid := types.SessionID("sess_test")

	sub := r.Subscribe(c.ID, sid, []string{"stdout"}, "test", connActor(), 0)
	r.Unsubscribe(sub.ID)

	if r.SubscriberCount(sid) != 0 {
		t.Errorf("count = %d, want 0", r.SubscriberCount(sid))
	}

	// Push should not reach (no subscribers)
	r.PushChunk(sid, "stdout", 1, "data")
	assertNoMsg(t, ch, 200*time.Millisecond)
}

// ─── Multi-subscriber fan-out ───────────────────────────────────────

// P0 T1: Two subscribers to the same session both receive stdout.
func TestRegistry_T1_TwoSubscribersBothReceive(t *testing.T) {
	r := NewRegistry()
	ch1 := make(chan []byte, 10)
	ch2 := make(chan []byte, 10)
	c1 := r.RegisterConn(ch1, connActor())
	c2 := r.RegisterConn(ch2, connActor())
	sid := types.SessionID("sess_fanout")

	r.Subscribe(c1.ID, sid, []string{"stdout"}, "test", connActor(), 0)
	r.Subscribe(c2.ID, sid, []string{"stdout"}, "test", connActor(), 0)

	r.PushChunk(sid, "stdout", 1, "hello")

	waitForCh(t, ch1, time.Second)
	waitForCh(t, ch2, time.Second)
}

// P0 T2: One subscriber disconnects, the other continues receiving.
func TestRegistry_T2_OneDisconnectOtherContinues(t *testing.T) {
	r := NewRegistry()
	ch1 := make(chan []byte, 10)
	ch2 := make(chan []byte, 10)
	c1 := r.RegisterConn(ch1, connActor())
	c2 := r.RegisterConn(ch2, connActor())
	sid := types.SessionID("sess_disconnect_one")

	r.Subscribe(c1.ID, sid, []string{"stdout"}, "test", connActor(), 0)
	r.Subscribe(c2.ID, sid, []string{"stdout"}, "test", connActor(), 0)

	// Disconnect c1
	r.UnregisterConn(c1.ID)

	r.PushChunk(sid, "stdout", 1, "still here")

	// c2 still gets it
	waitForCh(t, ch2, time.Second)

	// c1 must not
	assertNoMsg(t, ch1, 200*time.Millisecond)
}

// P0 T3: Old connection cleanup after new connection re-subscribes
// must not delete the new subscription.
func TestRegistry_T3_ReRegisterSurvivesOldCleanup(t *testing.T) {
	r := NewRegistry()
	chOld := make(chan []byte, 10)
	chNew := make(chan []byte, 10)
	sid := types.SessionID("sess_rereg")

	cOld := r.RegisterConn(chOld, connActor())
	r.Subscribe(cOld.ID, sid, []string{"stdout"}, "test", connActor(), 0)

	// New connection subscribes to same session
	cNew := r.RegisterConn(chNew, connActor())
	r.Subscribe(cNew.ID, sid, []string{"stdout"}, "test", connActor(), 0)

	// Old connection cleanup runs late
	r.UnregisterConn(cOld.ID)

	// Push must reach only the new connection
	r.PushChunk(sid, "stdout", 1, "after cleanup")

	waitForCh(t, chNew, time.Second)
	assertNoMsg(t, chOld, 200*time.Millisecond)
}

// P0 T4: Subscribe then replay — PushChunk after subscribe reaches.
func TestRegistry_T4_SubscribeThenPush(t *testing.T) {
	r := NewRegistry()
	ch := make(chan []byte, 10)
	c := r.RegisterConn(ch, connActor())
	sid := types.SessionID("sess_sub_then_push")

	r.Subscribe(c.ID, sid, []string{"stdout"}, "test", connActor(), 0)
	r.PushChunk(sid, "stdout", 1, "after subscribe")

	waitForCh(t, ch, time.Second)
}

// P0 T5: Stream type filtering — subscribing to stdout does
// not receive stderr.
func TestRegistry_T5_StreamTypeFilter(t *testing.T) {
	r := NewRegistry()
	ch := make(chan []byte, 10)
	c := r.RegisterConn(ch, connActor())
	sid := types.SessionID("sess_filter")

	r.Subscribe(c.ID, sid, []string{"stdout"}, "test", connActor(), 0)

	r.PushChunk(sid, "stderr", 1, "should not reach stdout subscriber")
	assertNoMsg(t, ch, 200*time.Millisecond)

	r.PushChunk(sid, "stdout", 2, "should reach")
	waitForCh(t, ch, time.Second)
}

// P0 T6: One subscriber's full channel does not block other subscribers.
func TestRegistry_T6_FullChannelDoesNotBlock(t *testing.T) {
	r := NewRegistry()
	chSlow := make(chan []byte, 1) // buffer size 1 — easily fills
	chFast := make(chan []byte, 10)
	cSlow := r.RegisterConn(chSlow, connActor())
	cFast := r.RegisterConn(chFast, connActor())
	sid := types.SessionID("sess_full_chan")

	r.Subscribe(cSlow.ID, sid, []string{"stdout"}, "test", connActor(), 0)
	r.Subscribe(cFast.ID, sid, []string{"stdout"}, "test", connActor(), 0)

	// Fill slow channel
	r.PushChunk(sid, "stdout", 1, "fill")
	r.PushChunk(sid, "stdout", 2, "overflow")

	// Fast channel must still receive
	waitForCh(t, chFast, time.Second)
}

// P0 T7: Process exit event reaches all subscribers.
func TestRegistry_T7_EventReachesAllSubscribers(t *testing.T) {
	r := NewRegistry()
	ch1 := make(chan []byte, 10)
	ch2 := make(chan []byte, 10)
	c1 := r.RegisterConn(ch1, connActor())
	c2 := r.RegisterConn(ch2, connActor())
	sid := types.SessionID("sess_event")

	r.Subscribe(c1.ID, sid, []string{"stdout"}, "test", connActor(), 0)
	r.Subscribe(c2.ID, sid, []string{"stdout"}, "test", connActor(), 0)

	r.PushSessionEvent(sid, 1, "exited", map[string]int{"exitCode": 0})

	d1 := waitForCh(t, ch1, time.Second)
	d2 := waitForCh(t, ch2, time.Second)

	if typ := msgType(t, d1); typ != "session.event" {
		t.Errorf("msg type = %s, want session.event", typ)
	}
	if typ := msgType(t, d2); typ != "session.event" {
		t.Errorf("msg type = %s, want session.event", typ)
	}
}

// ─── Legacy compatibility ───────────────────────────────────────────

func TestRegistry_NoConn(t *testing.T) {
	r := NewRegistry()
	sid := types.SessionID("sess_none")
	r.PushChunk(sid, "stdout", 1, "data") // no subs — no panic
}

func TestRegistry_PushChunk(t *testing.T) {
	r := NewRegistry()
	ch := make(chan []byte, 10)
	c := r.RegisterConn(ch, connActor())
	sid := types.SessionID("sess_chunk")
	r.Subscribe(c.ID, sid, []string{"stdout"}, "test", connActor(), 0)

	r.PushChunk(sid, "stdout", 1, "hello")

	data := waitForCh(t, ch, time.Second)
	if len(data) == 0 {
		t.Fatal("expected non-empty chunk message")
	}
	if msgType(t, data) != "stream.chunk" {
		t.Errorf("type = %s, want stream.chunk", msgType(t, data))
	}
}

func TestRegistry_Broadcast(t *testing.T) {
	r := NewRegistry()
	ch1 := make(chan []byte, 10)
	ch2 := make(chan []byte, 10)
	r.RegisterConn(ch1, connActor())
	r.RegisterConn(ch2, connActor())

	r.Broadcast(protocol.NewPong())

	waitForCh(t, ch1, time.Second)
	waitForCh(t, ch2, time.Second)
}

func TestRegistry_SubscriptionsBySession(t *testing.T) {
	r := NewRegistry()
	ch := make(chan []byte, 10)
	c := r.RegisterConn(ch, connActor())
	sid := types.SessionID("sess_list")

	r.Subscribe(c.ID, sid, []string{"stdout"}, "test", connActor(), 0)
	r.Subscribe(c.ID, sid, []string{"stderr"}, "test", connActor(), 0)

	subs := r.SubscriptionsBySession(sid)
	if len(subs) != 2 {
		t.Errorf("got %d subs, want 2", len(subs))
	}
}

func TestRegistry_SubscriptionsByConn(t *testing.T) {
	r := NewRegistry()
	ch := make(chan []byte, 10)
	c := r.RegisterConn(ch, connActor())
	sid := types.SessionID("sess_conn_sub")

	r.Subscribe(c.ID, sid, []string{"stdout"}, "test", connActor(), 0)

	subs := r.SubscriptionsByConn(c.ID)
	if len(subs) != 1 {
		t.Errorf("got %d subs, want 1", len(subs))
	}
	if subs[0].SessionID != sid {
		t.Errorf("sessionId = %s, want %s", subs[0].SessionID, sid)
	}
}

func TestRegistry_GetConn(t *testing.T) {
	r := NewRegistry()
	ch := make(chan []byte, 10)
	c := r.RegisterConn(ch, connActor())

	got := r.GetConn(c.ID)
	if got == nil {
		t.Fatal("expected non-nil connection")
	}
	if got.ID != c.ID {
		t.Errorf("id = %s, want %s", got.ID, c.ID)
	}

	// Non-existent
	if r.GetConn("nonexistent") != nil {
		t.Error("expected nil for nonexistent connection")
	}
}
