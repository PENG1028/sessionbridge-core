//go:build !windows && !linux && !darwin

package process

import "fmt"

// childrenOf returns direct child PIDs on unsupported platforms.
func childrenOf(pid int) ([]int, error) {
	return nil, nil
}

// descendantsOf returns all descendant PIDs on unsupported platforms.
func descendantsOf(pid int) ([]int, error) {
	return nil, nil
}

// signalByPID on unsupported platforms returns an error.
func signalByPID(pid int, signal string) error {
	return fmt.Errorf("process signaling not supported on this platform")
}
