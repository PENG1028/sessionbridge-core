//go:build windows

package process

import (
	"github.com/user/sessionnode/go-core/pkg/types"
)

// SpawnPTY falls back to pipe-based spawn on Windows.
// PTY (pseudo-terminal) is not available on this platform,
// so we use os/exec pipes which provide basic stdin/stdout I/O.
// cfg carries optional metadata; pass nil for defaults.
func (m *Manager) SpawnPTY(command string, args []string, cwd string, cols, rows int, cfg *SpawnConfig) (types.SessionID, error) {
	return m.Spawn(command, args, cwd, cfg)
}

// Resize is a no-op on Windows (pipe-based processes don't support PTY resize).
func (m *Manager) Resize(sid types.SessionID, cols, rows int) error {
	return nil
}
