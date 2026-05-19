package logs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// RotateWriter writes to a file and rotates when the file size limit is
// reached. It is safe for concurrent use.
type RotateWriter struct {
	mu       sync.Mutex
	dir      string
	baseName string
	maxSize  int64 // bytes
	maxFiles int
	file     *os.File
	written  int64
}

// NewRotateWriter creates a RotateWriter. maxSize is the threshold in bytes
// that triggers rotation. maxFiles is the number of rotated generations to
// keep (excluding the current file).
func NewRotateWriter(dir, baseName string, maxSize int64, maxFiles int) (*RotateWriter, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("rotate: mkdir %s: %w", dir, err)
	}

	path := filepath.Join(dir, baseName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("rotate: open %s: %w", path, err)
	}

	// Seed written counter from existing file.
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("rotate: stat %s: %w", path, err)
	}

	return &RotateWriter{
		dir:      dir,
		baseName: baseName,
		maxSize:  maxSize,
		maxFiles: maxFiles,
		file:     f,
		written:  info.Size(),
	}, nil
}

// Write implements io.Writer. It triggers rotation before writing when the
// accumulated written bytes plus the new data would exceed maxSize.
func (w *RotateWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Rotate before writing if adding this data would exceed the limit.
	if w.written+int64(len(p)) > w.maxSize {
		if err := w.rotate(); err != nil {
			return 0, fmt.Errorf("rotate: %w", err)
		}
	}

	n, err := w.file.Write(p)
	if err != nil {
		return n, err
	}
	w.written += int64(n)
	return n, nil
}

// Close implements io.Closer.
func (w *RotateWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}

// rotate performs the actual file rotation:
//  1. Close current file.
//  2. If maxFiles==0, reopen with truncation and return early.
//  3. Remove the oldest generation that exceeds maxFiles.
//  4. Shift existing rotated files upward: .i → .i+1 for i = maxFiles-1 down to 1.
//  5. Rename current file to baseName.1.
//  6. Open a new empty file at baseName.
//  7. Reset written counter.
//
// Must be called with w.mu held.
func (w *RotateWriter) rotate() error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return fmt.Errorf("close current: %w", err)
		}
		w.file = nil
	}

	currentPath := filepath.Join(w.dir, w.baseName)

	// maxFiles==0 means no history — just truncate the current file.
	if w.maxFiles == 0 {
		f, err := os.OpenFile(currentPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return fmt.Errorf("truncate %s: %w", currentPath, err)
		}
		w.file = f
		w.written = 0
		return nil
	}

	// Remove the oldest generation that exceeds maxFiles.
	oldestPath := filepath.Join(w.dir, fmt.Sprintf("%s.%d", w.baseName, w.maxFiles))
	os.Remove(oldestPath) // ignore error — file may not exist

	// Shift existing rotated files upward.
	for i := w.maxFiles - 1; i >= 1; i-- {
		src := filepath.Join(w.dir, fmt.Sprintf("%s.%d", w.baseName, i))
		dst := filepath.Join(w.dir, fmt.Sprintf("%s.%d", w.baseName, i+1))
		if _, err := os.Stat(src); err == nil {
			if err := os.Rename(src, dst); err != nil {
				return fmt.Errorf("rename %s -> %s: %w", src, dst, err)
			}
		}
	}

	// Rename current file to baseName.1.
	firstPath := filepath.Join(w.dir, w.baseName+".1")
	if err := os.Rename(currentPath, firstPath); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", currentPath, firstPath, err)
	}

	// Open a fresh file.
	f, err := os.OpenFile(currentPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("create new %s: %w", currentPath, err)
	}
	w.file = f
	w.written = 0

	return nil
}

// ListRotated returns the paths of all rotated (non-current) log files in
// ascending generation order (oldest first). This is a helper for
// administrative tasks and is not needed for normal operation.
func ListRotated(dir, baseName string) ([]string, error) {
	pattern := baseName + ".*"
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return nil, err
	}

	// Sort by generation number embedded in the suffix.
	sort.Slice(matches, func(i, j int) bool {
		return matches[i] < matches[j]
	})
	return matches, nil
}
