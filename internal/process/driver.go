package process

// PTYDriver handles platform-specific pseudo-terminal I/O.
// Each platform implementation lives in the corresponding build-tag-gated file
// (pty_unix.go, pty_windows.go) and provides a concrete driver.
//
// The driver is created by SpawnPTY and attached to Process.ptyDriver.
// Manager delegates WriteStdin, Resize, and cleanup to this interface,
// keeping the core process-management code platform-agnostic.
type PTYDriver interface {
	// Write writes data to the PTY's input (standard input of the child process).
	Write(data string) error

	// Resize changes the terminal window size.
	Resize(cols, rows int) error

	// Close releases all PTY resources.
	Close() error

	// PtyMode returns a short identifier for the PTY backend in use
	// ("conpty", "console", "unix-pty", etc.). Used for diagnostics.
	PtyMode() string
}
