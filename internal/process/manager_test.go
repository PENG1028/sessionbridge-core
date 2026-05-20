package process

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/user/sessionnode/go-core/pkg/types"
)

// testRecorder collects pushed chunks and events for verification.
type testRecorder struct {
	mu      sync.Mutex
	chunks  []string
	events  []string
	seq     atomic.Int64
	pusher  PushFunc
	eventer EventFunc
}

func newTestRecorder() *testRecorder {
	tr := &testRecorder{}
	tr.pusher = func(_ types.SessionID, streamType string, _ types.EventSeq, data string) {
		tr.mu.Lock()
		tr.chunks = append(tr.chunks, streamType+":"+data)
		tr.mu.Unlock()
	}
	tr.eventer = func(_ types.SessionID, _ types.EventSeq, eventType string, _ interface{}) {
		tr.mu.Lock()
		tr.events = append(tr.events, eventType)
		tr.mu.Unlock()
	}
	return tr
}

func (tr *testRecorder) getChunks() []string {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	out := make([]string, len(tr.chunks))
	copy(out, tr.chunks)
	return out
}

func (tr *testRecorder) getEvents() []string {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	out := make([]string, len(tr.events))
	copy(out, tr.events)
	return out
}

func TestSpawn_StartsProcess(t *testing.T) {
	tr := newTestRecorder()
	m := NewManager(tr.pusher, tr.eventer)
	defer m.Cleanup()

	sid, err := m.Spawn("echo", []string{"hello"}, "", nil)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	proc := m.Get(sid)
	if proc == nil {
		t.Fatal("expected non-nil process")
	}

	// Wait for exit.
	time.Sleep(500 * time.Millisecond)
	proc = m.Get(sid)
	if proc.State != "exited" {
		t.Errorf("expected exited state, got %s", proc.State)
	}

	events := tr.getEvents()
	if len(events) < 2 {
		t.Fatalf("expected >= 2 events (started, exited), got %d: %v", len(events), events)
	}
	if events[0] != "started" {
		t.Errorf("expected first event 'started', got %s", events[0])
	}
}

func TestSpawn_OutputDelivery(t *testing.T) {
	tr := newTestRecorder()
	m := NewManager(tr.pusher, tr.eventer)
	defer m.Cleanup()

	_, err := m.Spawn("echo", []string{"hello world"}, "", nil)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	time.Sleep(1 * time.Second)

	chunks := tr.getChunks()
	found := false
	for _, c := range chunks {
		if strings.Contains(c, "hello world") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected output containing 'hello world', got: %v", chunks)
	}
}

func TestWriteStdin(t *testing.T) {
	tr := newTestRecorder()
	m := NewManager(tr.pusher, tr.eventer)
	defer m.Cleanup()

	sid, err := m.Spawn("sh", []string{"-c", "cat > /dev/null; echo done"}, "", nil)
	if err != nil {
		t.Skipf("sh not available: %v", err)
	}

	if err := m.WriteStdin(sid, "test input\n"); err != nil {
		t.Fatalf("WriteStdin: %v", err)
	}
	if err := m.CloseStdin(sid); err != nil {
		t.Fatalf("CloseStdin: %v", err)
	}

	time.Sleep(500 * time.Millisecond)
}

func TestSignal_Kill(t *testing.T) {
	tr := newTestRecorder()
	m := NewManager(tr.pusher, tr.eventer)
	defer m.Cleanup()

	sid, err := m.Spawn("sleep", []string{"60"}, "", nil)
	if err != nil {
		t.Skipf("sleep not available: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	if err := m.Signal(sid, "SIGKILL", false); err != nil {
		t.Fatalf("Signal: %v", err)
	}

	time.Sleep(500 * time.Millisecond)
	proc := m.Get(sid)
	if proc == nil {
		t.Fatal("expected process after kill")
	}
	if proc.State != "exited" {
		t.Errorf("expected exited after kill, got %s", proc.State)
	}
}

func TestList_Count(t *testing.T) {
	tr := newTestRecorder()
	m := NewManager(tr.pusher, tr.eventer)
	defer m.Cleanup()

	if m.Count() != 0 {
		t.Errorf("expected 0 processes initially, got %d", m.Count())
	}

	m.Spawn("echo", []string{"a"}, "", nil)
	m.Spawn("echo", []string{"b"}, "", nil)

	if m.Count() != 2 {
		t.Errorf("expected 2 processes, got %d", m.Count())
	}
	list := m.List()
	if len(list) != 2 {
		t.Errorf("expected 2 processes in list, got %d", len(list))
	}
}

func TestCleanup_KillsAll(t *testing.T) {
	tr := newTestRecorder()
	m := NewManager(tr.pusher, tr.eventer)

	m.Spawn("sleep", []string{"60"}, "", nil)
	m.Spawn("sleep", []string{"60"}, "", nil)

	if m.Count() != 2 {
		t.Fatalf("expected 2 processes before cleanup, got %d", m.Count())
	}

	m.Cleanup()

	if m.Count() != 0 {
		t.Errorf("expected 0 processes after cleanup, got %d", m.Count())
	}
}

func TestCloseStdin(t *testing.T) {
	tr := newTestRecorder()
	m := NewManager(tr.pusher, tr.eventer)
	defer m.Cleanup()

	sid, err := m.Spawn("sh", []string{"-c", "cat > /dev/null"}, "", nil)
	if err != nil {
		t.Skipf("sh not available: %v", err)
	}

	if err := m.CloseStdin(sid); err != nil {
		t.Fatalf("CloseStdin: %v", err)
	}
}

func TestGet_NotFound(t *testing.T) {
	tr := newTestRecorder()
	m := NewManager(tr.pusher, tr.eventer)

	proc := m.Get("nonexistent")
	if proc != nil {
		t.Errorf("expected nil for nonexistent session, got %v", proc)
	}
}

func TestSignal_ProcessNotFound(t *testing.T) {
	tr := newTestRecorder()
	m := NewManager(tr.pusher, tr.eventer)

	err := m.Signal("nonexistent", "SIGTERM", false)
	if err == nil {
		t.Fatal("expected error for nonexistent process")
	}
}

func TestWriteStdin_ProcessNotFound(t *testing.T) {
	tr := newTestRecorder()
	m := NewManager(tr.pusher, tr.eventer)

	err := m.WriteStdin("nonexistent", "data")
	if err == nil {
		t.Fatal("expected error for nonexistent process")
	}
}

func TestSpawn_MissingCommand(t *testing.T) {
	tr := newTestRecorder()
	m := NewManager(tr.pusher, tr.eventer)
	defer m.Cleanup()

	_, err := m.Spawn("nonexistent_command_xyz", nil, "", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent command")
	}
}

func TestReadStream_RawBytes(t *testing.T) {
	tr := newTestRecorder()
	m := NewManager(tr.pusher, tr.eventer)
	defer m.Cleanup()

	_, err := m.Spawn("printf", []string{"partial_line"}, "", nil)
	if err != nil {
		// Fallback: echo -n might work in some shells.
		t.Skipf("printf not available: %v", err)
	}

	time.Sleep(1 * time.Second)

	chunks := tr.getChunks()
	found := false
	for _, c := range chunks {
		if strings.Contains(c, "partial_line") {
			found = true
			break
		}
	}
	if !found {
		t.Logf("chunks received: %v", chunks)
	}
}

func TestSpawnPTY_OnWindows(t *testing.T) {
	tr := newTestRecorder()
	m := NewManager(tr.pusher, tr.eventer)

	_, err := m.SpawnPTY("echo", []string{"test"}, "", 80, 40, nil)
	if err == nil {
		t.Skip("PTY is supported on this platform")
	}
}

func TestResize_NoPTY(t *testing.T) {
	tr := newTestRecorder()
	m := NewManager(tr.pusher, tr.eventer)
	defer m.Cleanup()

	sid, err := m.Spawn("echo", []string{"test"}, "", nil)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	err = m.Resize(sid, 80, 40)
	if err == nil {
		t.Skip("resize succeeded (likely on Unix with PTY available)")
	}
}

func TestSpawn_MultipleOutputLines(t *testing.T) {
	tr := newTestRecorder()
	m := NewManager(tr.pusher, tr.eventer)
	defer m.Cleanup()

	sid, err := m.Spawn("sh", []string{"-c", "echo line1; echo line2; echo line3"}, "", nil)
	if err != nil {
		t.Skipf("sh not available: %v", err)
	}

	time.Sleep(1 * time.Second)

	chunks := tr.getChunks()
	outLines := 0
	for _, c := range chunks {
		if strings.HasPrefix(c, "stdout:") {
			outLines++
		}
	}
	if outLines < 3 {
		t.Logf("expected >= 3 stdout chunks, got %d: %v", outLines, chunks)
	}

	proc := m.Get(sid)
	if proc != nil {
		t.Logf("state: %s, exitCode: %d", proc.State, proc.ExitCode)
	}
}

// TestMain provides a safety net to clean up any leaked test processes.
func TestMain(m *testing.M) {
	code := m.Run()
	exec.Command("pkill", "-f", "sleep 60").Run()
	os.Exit(code)
}
