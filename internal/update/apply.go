package update

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
)

// ApplyOptions controls the update application.
type ApplyOptions struct {
	DownloadURL   string // URL of the release asset tar.gz
	ExpectedHash  string // optional SHA-256 checksum
	CurrentExe    string // path to current binary (os.Executable())
	ReplaceTarget string // path to replace (usually == CurrentExe)
}

// Apply downloads, verifies, and replaces the current binary.
// It writes a .new file alongside the target, then atomically renames it.
func Apply(opts ApplyOptions) error {
	if opts.DownloadURL == "" {
		return fmt.Errorf("download URL is required")
	}
	if opts.ReplaceTarget == "" {
		return fmt.Errorf("replace target is required")
	}

	// Download to temp file
	tmpFile, err := os.CreateTemp("", "sessionbridge-update-*.tar.gz")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	resp, err := http.Get(opts.DownloadURL)
	if err != nil {
		tmpFile.Close()
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		tmpFile.Close()
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}

	hash := sha256.New()
	writer := io.MultiWriter(tmpFile, hash)

	if _, err := io.Copy(writer, resp.Body); err != nil {
		tmpFile.Close()
		return fmt.Errorf("download write: %w", err)
	}
	tmpFile.Close()

	// Verify checksum if provided
	if opts.ExpectedHash != "" {
		got := hex.EncodeToString(hash.Sum(nil))
		if got != opts.ExpectedHash {
			return fmt.Errorf("checksum mismatch: got %s, expected %s", got, opts.ExpectedHash)
		}
	}

	// Extract binary from tar.gz
	newPath := opts.ReplaceTarget + ".new"
	if err := extractBinary(tmpFile.Name(), newPath); err != nil {
		return fmt.Errorf("extract binary: %w", err)
	}

	// Replace target with new binary
	if err := replaceBinary(opts.ReplaceTarget, newPath); err != nil {
		return fmt.Errorf("replace binary: %w", err)
	}

	return nil
}

// extractBinary extracts the sessionnode binary from a goreleaser tar.gz.
func extractBinary(tarGzPath, outputPath string) error {
	f, err := os.Open(tarGzPath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gzr.Close()

	tarReader := tar.NewReader(gzr)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}

		// Look for the binary (sessionnode or sessionnode.exe)
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if header.Name != "sessionnode" && header.Name != "sessionnode.exe" {
			continue
		}

		// Write extracted binary
		out, err := os.Create(outputPath)
		if err != nil {
			return fmt.Errorf("create output: %w", err)
		}
		defer out.Close()

		if _, err := io.Copy(out, tarReader); err != nil {
			return fmt.Errorf("write binary: %w", err)
		}

		if err := os.Chmod(outputPath, 0755); err != nil {
			return fmt.Errorf("chmod: %w", err)
		}
		return nil
	}

	return fmt.Errorf("sessionnode binary not found in archive")
}

// replaceBinary atomically replaces the target binary.
func replaceBinary(target, newPath string) error {
	if runtime.GOOS == "windows" {
		// Windows: can't overwrite running exe, use swap via .old
		backup := target + ".old"
		os.Rename(target, backup) // best-effort
		if err := os.Rename(newPath, target); err != nil {
			// Recover: put backup back
			os.Rename(backup, target)
			return fmt.Errorf("windows rename: %w", err)
		}
		os.Remove(backup)
		return nil
	}

	// Unix: atomic rename
	if err := os.Rename(newPath, target); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	if err := os.Chmod(target, 0755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}
	return nil
}
