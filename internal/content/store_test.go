package content

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PENG1028/sessionbridge-core/pkg/types"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "trash")
	s := NewStore(dir)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return s
}

// TestStore_StoreAndRestore verifies basic store/restore round-trip.
func TestStore_StoreAndRestore(t *testing.T) {
	s := newTestStore(t)

	data := []byte("hello content store")
	ref, err := s.Store(data)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	if ref.Size != int64(len(data)) {
		t.Errorf("size = %d, want %d", ref.Size, len(data))
	}
	if ref.Hash == "" {
		t.Fatal("hash is empty")
	}

	restored, err := s.Restore(ref)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if string(restored) != string(data) {
		t.Errorf("restored = %q, want %q", string(restored), string(data))
	}
}

// TestStore_Dedup verifies content is deduplicated by hash.
func TestStore_Dedup(t *testing.T) {
	s := newTestStore(t)

	data := []byte("dedup me")
	ref1, err := s.Store(data)
	if err != nil {
		t.Fatalf("Store 1: %v", err)
	}
	ref2, err := s.Store(data)
	if err != nil {
		t.Fatalf("Store 2: %v", err)
	}

	if ref1.Hash != ref2.Hash {
		t.Error("expected same hash for same content")
	}
	if ref1.Size != ref2.Size {
		t.Error("expected same size for same content")
	}

	// Ref count should be 2
	if rc := s.RefCount(ref1.Hash); rc != 2 {
		t.Errorf("expected refCount=2, got %d", rc)
	}
}

// TestStore_ReleaseAndGC verifies reference counting and GC.
func TestStore_ReleaseAndGC(t *testing.T) {
	s := newTestStore(t)

	data1 := []byte("content one")
	data2 := []byte("content two")

	ref1, _ := s.Store(data1)
	ref2, _ := s.Store(data2)

	// Store same content again to inc ref count
	s.Store(data1)

	if rc := s.RefCount(ref1.Hash); rc != 2 {
		t.Errorf("expected refCount=2 for data1, got %d", rc)
	}

	// Release once
	s.Release(ref1.Hash)
	if rc := s.RefCount(ref1.Hash); rc != 1 {
		t.Errorf("expected refCount=1 after release, got %d", rc)
	}

	// GC should not remove anything (refCount > 0)
	gc1 := s.GC()
	if gc1.Removed != 0 {
		t.Errorf("expected 0 removed by GC (refs still active), got %d", gc1.Removed)
	}

	// Release again to make refCount go to 0
	s.Release(ref1.Hash)
	if rc := s.RefCount(ref1.Hash); rc != 0 {
		t.Errorf("expected refCount=0 after second release, got %d", rc)
	}

	// Now GC should remove data1 but not data2
	gc2 := s.GC()
	if gc2.Removed != 1 {
		t.Errorf("expected 1 blob removed by GC, got %d", gc2.Removed)
	}
	if gc2.Freed <= 0 {
		t.Errorf("expected freed bytes > 0, got %d", gc2.Freed)
	}

	// Verify data1 blob file was deleted
	blobPath := filepath.Join(s.baseDir, ref1.Hash[:2], ref1.Hash)
	if _, err := os.Stat(blobPath); !os.IsNotExist(err) {
		t.Errorf("expected blob %s to be deleted, stat err: %v", ref1.Hash, err)
	}

	// data2 should still exist
	blobPath2 := filepath.Join(s.baseDir, ref2.Hash[:2], ref2.Hash)
	if _, err := os.Stat(blobPath2); os.IsNotExist(err) {
		t.Errorf("expected blob %s to still exist", ref2.Hash)
	}
}

// TestStore_TooLarge verifies MaxArtifactSize enforcement.
func TestStore_TooLarge(t *testing.T) {
	s := newTestStore(t)

	data := make([]byte, MaxArtifactSize+1)
	_, err := s.Store(data)
	if err != ErrTooLarge {
		t.Errorf("expected ErrTooLarge, got %v", err)
	}
}

// TestStore_Persistence verifies ref counts survive store restart.
func TestStore_Persistence(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "trash")

	s1 := NewStore(dir)
	if err := s1.Load(); err != nil {
		t.Fatalf("Load s1: %v", err)
	}

	data := []byte("persist test")
	ref, _ := s1.Store(data)
	s1.Store(data) // inc ref count
	s1.Persist()

	// Reopen
	s2 := NewStore(dir)
	if err := s2.Load(); err != nil {
		t.Fatalf("Load s2: %v", err)
	}

	if rc := s2.RefCount(ref.Hash); rc != 2 {
		t.Errorf("expected refCount=2 after reload, got %d", rc)
	}

	restored, err := s2.Restore(ref)
	if err != nil {
		t.Fatalf("Restore after reload: %v", err)
	}
	if string(restored) != string(data) {
		t.Errorf("restored = %q, want %q", string(restored), string(data))
	}
}

// TestStore_FileMode verifies stored blobs are read-only.
func TestStore_FileMode(t *testing.T) {
	s := newTestStore(t)

	ref, _ := s.Store([]byte("readonly test"))
	info, err := os.Stat(ref.StoredAt)
	if err != nil {
		t.Fatalf("Stat blob: %v", err)
	}

	// Mode should be 0400 (read-only for owner)
	mode := info.Mode().Perm()
	if mode&0200 != 0 {
		t.Errorf("expected blob to be read-only, got mode %o", mode)
	}
}

// TestStore_RestoreWithContentRef verifies Restore uses the ContentRef object.
func TestStore_RestoreWithContentRef(t *testing.T) {
	s := newTestStore(t)

	ref, err := s.Store([]byte("restore test"))
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	// Create a new ref pointing to the same data
	newRef := &types.ContentRef{
		Hash:     ref.Hash,
		Size:     ref.Size,
		StoredAt: ref.StoredAt,
	}

	data, err := s.Restore(newRef)
	if err != nil {
		t.Fatalf("Restore with newRef: %v", err)
	}
	if string(data) != "restore test" {
		t.Errorf("got %q, want %q", string(data), "restore test")
	}
}

// TestStore_ConcurrentAccess verifies concurrent store/release safety.
func TestStore_ConcurrentAccess(t *testing.T) {
	s := newTestStore(t)

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func(n int) {
			for j := 0; j < 10; j++ {
				data := []byte{byte(n), byte(j)}
				ref, _ := s.Store(data)
				s.Release(ref.Hash)
			}
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	// GC after concurrent access — should not panic
	gc := s.GC()
	t.Logf("GC after concurrent access: removed %d, freed %d bytes", gc.Removed, gc.Freed)
}
