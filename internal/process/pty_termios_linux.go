//go:build linux

package process

import "syscall"

// ioctl TCGETS/TCSETS constants — Linux uses these names.
var ioctlTCGETS = uintptr(syscall.TCGETS)
var ioctlTCSETS = uintptr(syscall.TCSETS)
