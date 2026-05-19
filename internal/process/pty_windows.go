//go:build windows

package process

import (
	"fmt"

	"github.com/user/sessionnode/go-core/pkg/types"
)

// SpawnPTY is not supported on Windows.
func (m *Manager) SpawnPTY(command string, args []string, cwd string, cols, rows int) (types.SessionID, error) {
	return "", fmt.Errorf("PTY not supported on Windows")
}

// Resize is not supported on Windows.
func (m *Manager) Resize(sid types.SessionID, cols, rows int) error {
	return fmt.Errorf("PTY not supported on Windows")
}
