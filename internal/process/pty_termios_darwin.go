//go:build darwin

package process

import "syscall"

// ioctl TCGETS/TCSETS constants — Darwin (macOS) uses TIOCGETA/TIOCSETA.
var ioctlTCGETS = uintptr(syscall.TIOCGETA)
var ioctlTCSETS = uintptr(syscall.TIOCSETA)
