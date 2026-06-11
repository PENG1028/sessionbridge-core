package oplog

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PENG1028/sessionbridge-core/internal/content"
	"github.com/PENG1028/sessionbridge-core/pkg/types"
)

func newRollbackEngine(t *testing.T) *RollbackEngine {
	t.Helper()
	dir := t.TempDir()
	opLog := NewStore(dir, WithChunkSize(10), WithMaxRecords(500))
	t.Cleanup(func() { opLog.Close() })
	if err := opLog.Load(); err != nil {
		t.Fatalf("oplog load: %v", err)
	}
	contentStore := content.NewStore(filepath.Join(dir, "trash"))
	if err := contentStore.Load(); err != nil {
		t.Fatalf("content load: %v", err)
	}
	return NewRollbackEngine(opLog, contentStore)
}

func TestDryRun_ContentOp(t *testing.T) {
	engine := newRollbackEngine(t)

	tmpFile := filepath.Join(t.TempDir(), "test.txt")
	os.WriteFile(tmpFile, []byte("original"), 0644)
	data, _ := os.ReadFile(tmpFile)
	ref, _ := engine.Content.Store(data)

	op := &types.Operation{
		OpID:       "op_test_1",
		Class:      types.OpClassContent,
		Capability: "fs.write",
		ContentBefore: &types.ContentRef{
			Path: tmpFile, Hash: ref.Hash, Size: int64(len(data)), StoredAt: ref.StoredAt,
		},
	}
	engine.OpLog.Append(op)

	preview, err := engine.DryRun("op_test_1")
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if len(preview.WillRestore) != 1 {
		t.Errorf("expected 1 file to restore, got %d", len(preview.WillRestore))
	}
	if preview.NotSupported {
		t.Error("expected fs.write to have inverse registered")
	}
}

func TestDryRun_AlreadyRolledBack(t *testing.T) {
	engine := newRollbackEngine(t)

	op := &types.Operation{
		OpID:         "op_already",
		Class:        types.OpClassState,
		Capability:   "run.create",
		RolledBackAt: time.Now().UnixMilli(),
	}
	engine.OpLog.Append(op)

	preview, err := engine.DryRun("op_already")
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if preview.Reason != "already rolled back" {
		t.Errorf("expected 'already rolled back', got %q", preview.Reason)
	}
}

func TestDryRun_NoInverse(t *testing.T) {
	engine := newRollbackEngine(t)

	op := &types.Operation{
		OpID:       "op_no_inverse",
		Class:      types.OpClassState,
		Capability: "nonexistent.capability",
	}
	engine.OpLog.Append(op)

	preview, err := engine.DryRun("op_no_inverse")
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if !preview.NotSupported {
		t.Error("expected NotSupported for unknown capability")
	}
}

func TestDryRun_ArtifactOp(t *testing.T) {
	engine := newRollbackEngine(t)

	op := &types.Operation{
		OpID:       "op_dl",
		Class:      types.OpClassArtifact,
		Capability: "download",
		Artifacts:  []types.ArtifactRef{{Path: "/tmp/pkg.tar.gz", Size: 1024}},
	}
	engine.OpLog.Append(op)

	preview, err := engine.DryRun("op_dl")
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if len(preview.WillDelete) != 1 {
		t.Errorf("expected 1 file to delete, got %d", len(preview.WillDelete))
	}
	// download has an inverse registered — NotSupported should be false
	if preview.NotSupported {
		t.Errorf("download inverse is registered, NotSupported should be false")
	}
}

func TestRollback_FsWrite(t *testing.T) {
	engine := newRollbackEngine(t)

	tmpFile := filepath.Join(t.TempDir(), "rollback_test.txt")
	os.WriteFile(tmpFile, []byte("original content"), 0644)

	data, _ := os.ReadFile(tmpFile)
	ref, err := engine.Content.Store(data)
	if err != nil {
		t.Fatalf("Content.Store: %v", err)
	}

	os.WriteFile(tmpFile, []byte("new content"), 0644)

	op := &types.Operation{
		OpID:       "op_fs_1",
		Class:      types.OpClassContent,
		Capability: "fs.write",
		ContentBefore: &types.ContentRef{
			Path: tmpFile, Hash: ref.Hash, Size: ref.Size, StoredAt: ref.StoredAt,
		},
	}
	engine.OpLog.Append(op)

	// Verify file has new content
	currentData, _ := os.ReadFile(tmpFile)
	if string(currentData) != "new content" {
		t.Fatalf("expected new content before rollback, got %q", currentData)
	}

	// Rollback
	rolled, err := engine.Rollback("op_fs_1")
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if rolled.RolledBackAt == 0 {
		t.Error("RolledBackAt should be set")
	}

	// Verify original content restored
	restoredData, _ := os.ReadFile(tmpFile)
	if string(restoredData) != "original content" {
		t.Errorf("expected restored content %q, got %q", "original content", string(restoredData))
	}

	// Verify rollback event recorded
	events := engine.OpLog.Query(types.OpFilter{Capability: "_rollback"})
	if len(events) != 1 {
		t.Errorf("expected 1 rollback event, got %d", len(events))
	}
}

func TestRollback_AlreadyRolledBack(t *testing.T) {
	engine := newRollbackEngine(t)
	op := &types.Operation{
		OpID:         "op_twice",
		Class:        types.OpClassState,
		Capability:   "run.create",
		RolledBackAt: time.Now().UnixMilli(),
	}
	engine.OpLog.Append(op)

	_, err := engine.Rollback("op_twice")
	if err != ErrAlreadyRolledBack {
		t.Errorf("expected ErrAlreadyRolledBack, got %v", err)
	}
}

func TestRollback_NotFound(t *testing.T) {
	engine := newRollbackEngine(t)
	_, err := engine.Rollback("op_nonexistent")
	if err != ErrOpNotFound {
		t.Errorf("expected ErrOpNotFound, got %v", err)
	}
}

func TestRollbackRange(t *testing.T) {
	engine := newRollbackEngine(t)

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(tmpFile, []byte("original"), 0644)
	data, _ := os.ReadFile(tmpFile)
	ref, _ := engine.Content.Store(data)

	base := time.Now()

	op1 := &types.Operation{
		OpID: "op_r1", Class: types.OpClassArtifact, Capability: "download",
		Artifacts: []types.ArtifactRef{{Path: "/tmp/pkg1.tar.gz", Size: 100}},
		Timestamp: base.UnixMilli(),
	}
	engine.OpLog.Append(op1)

	time.Sleep(2 * time.Millisecond)
	mid := time.Now()

	op2 := &types.Operation{
		OpID: "op_r2", Class: types.OpClassContent, Capability: "fs.write",
		ContentBefore: &types.ContentRef{Path: tmpFile, Hash: ref.Hash, Size: ref.Size, StoredAt: ref.StoredAt},
		Timestamp:     mid.UnixMilli(),
	}
	engine.OpLog.Append(op2)

	time.Sleep(2 * time.Millisecond)
	end := time.Now()

	rolled, err := engine.RollbackRange(base, end)
	if err != nil {
		t.Fatalf("RollbackRange: %v", err)
	}
	if len(rolled) != 2 {
		t.Errorf("expected 2 ops rolled back, got %d", len(rolled))
	}
}

func TestRollbackRange_EmptyRange(t *testing.T) {
	engine := newRollbackEngine(t)
	rolled, err := engine.RollbackRange(time.Now(), time.Now())
	if err != nil {
		t.Fatalf("RollbackRange on empty range: %v", err)
	}
	if len(rolled) != 0 {
		t.Errorf("expected 0 ops in empty range, got %d", len(rolled))
	}
}

func TestVerifyArtifacts(t *testing.T) {
	engine := newRollbackEngine(t)

	tmpFile := filepath.Join(t.TempDir(), "artifact.txt")
	os.WriteFile(tmpFile, []byte("present"), 0644)

	engine.OpLog.Append(&types.Operation{
		OpID: "op_ok", Class: types.OpClassArtifact, Capability: "download",
		Artifacts: []types.ArtifactRef{{Path: tmpFile, Size: 7}},
	})
	engine.OpLog.Append(&types.Operation{
		OpID: "op_missing", Class: types.OpClassArtifact, Capability: "download",
		Artifacts: []types.ArtifactRef{{Path: "/tmp/nonexistent_xyz", Size: 100}},
	})

	reports := engine.VerifyArtifacts()
	if len(reports) != 2 {
		t.Fatalf("expected 2 reports, got %d", len(reports))
	}

	var okCount, missingCount int
	for _, r := range reports {
		switch r.Status {
		case "ok":
			okCount++
		case "missing":
			missingCount++
		}
	}
	if okCount != 1 {
		t.Errorf("expected 1 ok, got %d", okCount)
	}
	if missingCount != 1 {
		t.Errorf("expected 1 missing, got %d", missingCount)
	}
}

func TestRegisterInverse(t *testing.T) {
	customCalled := false
	RegisterInverse("custom.test", func(op *types.Operation, engine *RollbackEngine) error {
		customCalled = true
		return nil
	})

	engine := newRollbackEngine(t)
	op := &types.Operation{
		OpID: "op_custom", Class: types.OpClassState, Capability: "custom.test",
	}
	engine.OpLog.Append(op)

	_, err := engine.Rollback("op_custom")
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if !customCalled {
		t.Error("custom inverse was not called")
	}
}
