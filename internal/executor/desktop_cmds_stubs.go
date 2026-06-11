//go:build !windows

package executor

import (
	"fmt"
	"image"
)

// Stub implementations for non-Windows platforms.
// The actual dispatch goes through runtime.GOOS switches in desktop_cmds.go,
// so these are only reached if the switch statement logic changes unexpectedly.

func readClipboardWindows() (string, error) {
	return "", fmt.Errorf("clipboard (windows) not available on this platform")
}

func writeClipboardWindows(_ string) error {
	return fmt.Errorf("clipboard (windows) not available on this platform")
}

func captureScreenWindows(_ int) (*image.RGBA, error) {
	return nil, fmt.Errorf("screenshot (windows) not available on this platform")
}

func keyboardWindows(_ string, _ []string) error {
	return fmt.Errorf("keyboard (windows) not available on this platform")
}

func mouseWindows(_ string, _, _ int, _ string, _ int) error {
	return fmt.Errorf("mouse (windows) not available on this platform")
}

func typeTextWindows(_ string) error {
	return fmt.Errorf("text input (windows) not available on this platform")
}
