package platform

import (
	"runtime"
	"testing"
)

func TestCurrentOS(t *testing.T) {
	p := Current()
	if p.OS == "" {
		t.Error("Current().OS should not be empty")
	}
	if p.OS == "unknown" && runtime.GOOS != "android" && runtime.GOOS != "ios" && runtime.GOOS != "js" {
		// non-standard GOOS should map to unknown
		// On standard platforms this should match
		if runtime.GOOS == "windows" || runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
			t.Errorf("Current().OS = %q, expected %q", p.OS, runtime.GOOS)
		}
	}
}

func TestCurrentArch(t *testing.T) {
	p := Current()
	if p.Arch == "" {
		t.Error("Current().Arch should not be empty")
	}
}

func TestCurrentRuntime(t *testing.T) {
	p := Current()
	if p.Runtime == "" {
		t.Error("Current().Runtime should not be empty")
	}
	switch runtime.GOOS {
	case "windows", "linux", "darwin":
		if p.Runtime != "desktop" {
			t.Errorf("Current().Runtime = %q, expected desktop on %s", p.Runtime, runtime.GOOS)
		}
	case "android", "ios":
		if p.Runtime != "mobile" {
			t.Errorf("Current().Runtime = %q, expected mobile on %s", p.Runtime, runtime.GOOS)
		}
	case "js":
		if p.Runtime != "browser" {
			t.Errorf("Current().Runtime = %q, expected browser on js", p.Runtime)
		}
	}
}

func TestIsDesktop(t *testing.T) {
	tests := []struct {
		platform RuntimePlatform
		expected bool
	}{
		{RuntimePlatform{OS: "windows", Arch: "amd64", Runtime: "desktop"}, true},
		{RuntimePlatform{OS: "linux", Arch: "arm64", Runtime: "server"}, true},
		{RuntimePlatform{OS: "darwin", Arch: "amd64", Runtime: "desktop"}, true},
		{RuntimePlatform{OS: "android", Arch: "arm64", Runtime: "mobile"}, false},
		{RuntimePlatform{OS: "ios", Arch: "arm64", Runtime: "mobile"}, false},
		{RuntimePlatform{OS: "unknown", Arch: "unknown", Runtime: "unknown"}, false},
	}
	for _, tt := range tests {
		got := tt.platform.IsDesktop()
		if got != tt.expected {
			t.Errorf("IsDesktop(%+v) = %v, want %v", tt.platform, got, tt.expected)
		}
	}
}

func TestIsMobile(t *testing.T) {
	tests := []struct {
		platform RuntimePlatform
		expected bool
	}{
		{RuntimePlatform{OS: "windows", Arch: "amd64", Runtime: "desktop"}, false},
		{RuntimePlatform{OS: "linux", Arch: "arm64", Runtime: "desktop"}, false},
		{RuntimePlatform{OS: "android", Arch: "arm64", Runtime: "mobile"}, true},
		{RuntimePlatform{OS: "ios", Arch: "arm64", Runtime: "mobile"}, true},
		{RuntimePlatform{OS: "unknown", Arch: "unknown", Runtime: "browser"}, false},
	}
	for _, tt := range tests {
		got := tt.platform.IsMobile()
		if got != tt.expected {
			t.Errorf("IsMobile(%+v) = %v, want %v", tt.platform, got, tt.expected)
		}
	}
}
