package platform

import "runtime"

// RuntimePlatform describes the execution environment: OS, architecture, and runtime context.
type RuntimePlatform struct {
	OS      string // windows, linux, darwin, android, ios, unknown
	Arch    string // amd64, arm64, etc.
	Runtime string // server, desktop, mobile, browser, unknown
}

// Current returns the platform the process is running on, detected from Go runtime.
func Current() RuntimePlatform {
	p := RuntimePlatform{
		OS:   normalizeOS(runtime.GOOS),
		Arch: runtime.GOARCH,
	}
	switch runtime.GOOS {
	case "windows", "linux", "darwin":
		p.Runtime = "desktop"
	case "android", "ios":
		p.Runtime = "mobile"
	case "js":
		p.Runtime = "browser"
	default:
		p.Runtime = "unknown"
	}
	return p
}

// IsDesktop returns true when the platform is a desktop/server OS.
func (p RuntimePlatform) IsDesktop() bool {
	return p.Runtime == "desktop" || p.Runtime == "server"
}

// IsMobile returns true when the platform is a mobile OS.
func (p RuntimePlatform) IsMobile() bool {
	return p.Runtime == "mobile"
}

// normalizeOS maps known GOOS values to the canonical OS strings used in the Matrix.
func normalizeOS(goos string) string {
	switch goos {
	case "windows", "linux", "darwin", "android", "ios":
		return goos
	default:
		return "unknown"
	}
}
