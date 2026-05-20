package platform

import "runtime"

type Platform string

const (
	Windows Platform = "windows"
	Linux   Platform = "linux"
	Darwin  Platform = "darwin"
	Unknown Platform = "unknown"
)

func Current() Platform {
	switch runtime.GOOS {
	case "windows":
		return Windows
	case "linux":
		return Linux
	case "darwin":
		return Darwin
	default:
		return Unknown
	}
}

func (p Platform) String() string { return string(p) }
