package oplog

import (
	"github.com/PENG1028/sessionbridge-core/internal/content"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PENG1028/sessionbridge-core/pkg/types"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s := NewStore(dir, WithChunkSize(5), WithMaxRecords(500))
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return s
}

// newSmallStore creates a store with aggressive truncation for testing truncation behavior.
func newSmallStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s := NewStore(dir, WithChunkSize(5), WithMaxRecords(50))
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return s
}

func mkOp(capability string, class types.OpClass) *types.Operation {
	return &types.Operation{
		Capability: capability,
		Class:      class,
		Actor:      types.Actor{Type: "web", ID: "tester"},
	}
}

// TestStore_AppendAndGet verifies basic write and read.
func TestStore_AppendAndGet(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	op := mkOp("fs.write", types.OpClassContent)
	opID, err := s.Append(op)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if !opID.Valid() {
		t.Fatal("expected valid OpID")
	}

	got := s.Get(opID)
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Capability != "fs.write" {
		t.Errorf("capability = %q, want %q", got.Capability, "fs.write")
	}
	if got.Class != types.OpClassContent {
		t.Errorf("class = %q, want %q", got.Class, types.OpClassContent)
	}
}

// TestStore_Get_NotFound verifies Get returns nil for missing ID.
func TestStore_Get_NotFound(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	got := s.Get("op_nonexistent")
	if got != nil {
		t.Fatal("expected nil for nonexistent op")
	}
}

// TestStore_AppendMultiple verifies multiple appends and correct ordering.
func TestStore_AppendMultiple(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	ids := make([]types.OpID, 10)
	for i := 0; i < 10; i++ {
		op := mkOp("fs.write", types.OpClassContent)
		id, err := s.Append(op)
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		ids[i] = id
	}

	// Replay from the beginning
	all := s.Replay("")
	if len(all) != 10 {
		t.Errorf("expected 10 ops, got %d", len(all))
	}
}

// TestStore_ChunkRotation verifies chunk file rotation and sequential reading.
func TestStore_ChunkRotation(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	// Write enough to trigger chunk rotation (chunkSize=5)
	for i := 0; i < 12; i++ {
		op := mkOp("fs.write", types.OpClassContent)
		if _, err := s.Append(op); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	// Read all back
	all := s.Replay("")
	if len(all) != 12 {
		t.Errorf("expected 12 ops after rotation, got %d", len(all))
	}

	// Verify chunk files exist
	entries, _ := os.ReadDir(s.baseDir)
	var chunks int
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".jsonl" {
			chunks++
		}
	}
	if chunks < 2 {
		t.Errorf("expected at least 2 chunk files, got %d", chunks)
	}
}

// TestStore_ReplayFromID verifies partial replay.
func TestStore_ReplayFromID(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	var thirdID types.OpID
	for i := 0; i < 5; i++ {
		op := mkOp("fs.write", types.OpClassContent)
		id, err := s.Append(op)
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		if i == 2 {
			thirdID = id
		}
	}

	// Replay from third op
	partial := s.Replay(thirdID)
	if len(partial) != 3 {
		t.Errorf("expected 3 ops from thirdID, got %d", len(partial))
	}
	if partial[0].OpID != thirdID {
		t.Errorf("expected first op to be %q, got %q", thirdID, partial[0].OpID)
	}
}

// TestStore_QueryByClass verifies Query filtering.
func TestStore_QueryByClass(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	s.Append(mkOp("fs.write", types.OpClassContent))
	s.Append(mkOp("session.create", types.OpClassState))
	s.Append(mkOp("download", types.OpClassArtifact))

	contentOps := s.Query(types.OpFilter{
		Classes: []types.OpClass{types.OpClassContent},
	})
	if len(contentOps) != 1 {
		t.Errorf("expected 1 content op, got %d", len(contentOps))
	}

	stateOps := s.Query(types.OpFilter{
		Classes: []types.OpClass{types.OpClassState},
	})
	if len(stateOps) != 1 {
		t.Errorf("expected 1 state op, got %d", len(stateOps))
	}

	// Query for both content and state
	multiOps := s.Query(types.OpFilter{
		Classes: []types.OpClass{types.OpClassContent, types.OpClassState},
	})
	if len(multiOps) != 2 {
		t.Errorf("expected 2 ops (content+state), got %d", len(multiOps))
	}
}

// TestStore_QueryByCapability verifies Query filtering by capability name.
func TestStore_QueryByCapability(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	s.Append(mkOp("fs.write", types.OpClassContent))
	s.Append(mkOp("fs.write", types.OpClassContent))
	s.Append(mkOp("run.create", types.OpClassState))

	fsOps := s.Query(types.OpFilter{
		Capability: "fs.write",
	})
	if len(fsOps) != 2 {
		t.Errorf("expected 2 fs.write ops, got %d", len(fsOps))
	}
}

// TestStore_QueryTimeRange verifies time-range filtering.
func TestStore_QueryTimeRange(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	s.Append(mkOp("op1", types.OpClassState))
	time.Sleep(10 * time.Millisecond)
	mid := time.Now().UnixMilli()
	time.Sleep(10 * time.Millisecond)
	s.Append(mkOp("op3", types.OpClassState))

	// Query after mid point
	ops := s.Query(types.OpFilter{
		FromTime: mid,
	})
	if len(ops) != 1 {
		t.Errorf("expected 1 op after mid, got %d", len(ops))
	}
}

// TestStore_QueryLimit verifies the limit parameter.
func TestStore_QueryLimit(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	for i := 0; i < 10; i++ {
		s.Append(mkOp("fs.write", types.OpClassContent))
	}

	limited := s.Query(types.OpFilter{Limit: 3})
	if len(limited) != 3 {
		t.Errorf("expected 3 ops with limit=3, got %d", len(limited))
	}
}

// TestStore_Truncate verifies that old operations are removed.
func TestStore_Truncate(t *testing.T) {
	s := newSmallStore(t)
	defer s.Close()

	// Write more than maxRecords (50) to trigger truncation
	for i := 0; i < 60; i++ {
		op := mkOp("fs.write", types.OpClassContent)
		if _, err := s.Append(op); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	all := s.Replay("")
	if len(all) > 50 {
		t.Errorf("expected <=50 ops after truncation, got %d", len(all))
	}
	if len(all) == 0 {
		t.Error("expected some ops to remain after truncation")
	}
}

// TestStore_LoadFromDisk verifies persistence across Load cycles.
func TestStore_LoadFromDisk(t *testing.T) {
	dir := t.TempDir()

	// Create store, write ops, close
	s1 := NewStore(dir, WithChunkSize(10))
	if err := s1.Load(); err != nil {
		t.Fatalf("Load s1: %v", err)
	}
	for i := 0; i < 5; i++ {
		s1.Append(mkOp("persist-test", types.OpClassState))
	}
	s1.Close()

	// Reopen with a new store from the same directory
	s2 := NewStore(dir, WithChunkSize(10))
	if err := s2.Load(); err != nil {
		t.Fatalf("Load s2: %v", err)
	}
	defer s2.Close()

	all := s2.Replay("")
	if len(all) != 5 {
		t.Errorf("expected 5 persisted ops, got %d", len(all))
	}
}

// TestStore_ConcurrentAppend verifies concurrent safety.
func TestStore_ConcurrentAppend(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 10; j++ {
				s.Append(mkOp("concurrent", types.OpClassState))
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}

	all := s.Replay("")
	if len(all) != 100 {
		t.Errorf("expected 100 ops after concurrent appends, got %d", len(all))
	}
}

// TestStore_QueryByActorType verifies actor-type filtering.
func TestStore_QueryByActorType(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	op1 := &types.Operation{Capability: "config.set", Class: types.OpClassContent, Actor: types.Actor{Type: "web", ID: "u1"}}
	op2 := &types.Operation{Capability: "run.create", Class: types.OpClassState, Actor: types.Actor{Type: "cli", ID: "admin"}}
	s.Append(op1)
	s.Append(op2)

	webOps := s.Query(types.OpFilter{ActorType: "web"})
	if len(webOps) != 1 {
		t.Errorf("expected 1 web actor op, got %d", len(webOps))
	}
}

// TestStore_EmptyStore verifies a fresh store behaves correctly.
func TestStore_EmptyStore(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	all := s.Replay("")
	if len(all) != 0 {
		t.Errorf("expected 0 ops, got %d", len(all))
	}

	ops := s.Query(types.OpFilter{Limit: 10})
	if len(ops) != 0 {
		t.Errorf("expected 0 results, got %d", len(ops))
	}

	got := s.Get("op_nonexistent")
	if got != nil {
		t.Fatal("expected nil for empty store get")
	}
}


// TestStore_TruncateReleasesContentRefs verifies that truncating the OpLog
// releases ContentStore references for removed operations.
func TestStore_TruncateReleasesContentRefs(t *testing.T) {
	dir := t.TempDir()
	csDir := t.TempDir()

	cs := content.NewStore(csDir)
	if err := cs.Load(); err != nil {
		t.Fatalf("content load: %v", err)
	}

	s := NewStore(dir, WithChunkSize(3), WithMaxRecords(5), WithContentStore(cs))
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer s.Close()

	// Store content and get refs
	data := []byte("content for truncation test")
	ref, _ := cs.Store(data)
	if rc := cs.RefCount(ref.Hash); rc != 1 {
		t.Fatalf("expected refCount=1 after store, got %d", rc)
	}

	// Append more ops to push the content op past maxRecords
	// Write ops with content refs
	for i := 0; i < 3; i++ {
		op := mkOp("fs.write", types.OpClassContent)
		op.ContentBefore = &types.ContentRef{
			Path: "/tmp/test.txt", Hash: ref.Hash, Size: ref.Size, StoredAt: ref.StoredAt,
		}
		s.Append(op)
	}

	// Write filler ops to trigger truncation
	for i := 0; i < 10; i++ {
		s.Append(mkOp("session.create", types.OpClassState))
	}

	// After truncation, the content ref should have been released
	// (refCount decreased from the truncation of the first ops)
	finalRefs := cs.RefCount(ref.Hash)
	t.Logf("content ref count after truncation: %d", finalRefs)
	// At minimum, refCount should be < 3 (the content ops were truncated)
	if finalRefs >= 3 {
		t.Errorf("expected refCount < 3 after truncation, got %d", finalRefs)
	}
}
