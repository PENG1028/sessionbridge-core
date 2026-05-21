package capability

import (
	"testing"

	"github.com/user/sessionnode/go-core/internal/platform"
)

func desktop() platform.RuntimePlatform {
	return platform.RuntimePlatform{OS: "windows", Arch: "amd64", Runtime: "desktop"}
}

func linux() platform.RuntimePlatform {
	return platform.RuntimePlatform{OS: "linux", Arch: "amd64", Runtime: "desktop"}
}

func darwin() platform.RuntimePlatform {
	return platform.RuntimePlatform{OS: "darwin", Arch: "arm64", Runtime: "desktop"}
}

func mobile() platform.RuntimePlatform {
	return platform.RuntimePlatform{OS: "android", Arch: "arm64", Runtime: "mobile"}
}

func unknownPlat() platform.RuntimePlatform {
	return platform.RuntimePlatform{OS: "unknown", Arch: "unknown", Runtime: "unknown"}
}

func TestSupportDeclared(t *testing.T) {
	r := Resolver{Platform: desktop()}
	caps := []string{
		"session.create", "session.destroy", "session.list", "session.info", "session.get", "session.stop",
		"stream.subscribe", "stream.write", "stream.list", "stream.replay", "stream.tail",
		"process.spawn", "process.signal", "process.resize", "process.list",
		"fs.read", "fs.write", "fs.list", "fs.mkdir", "fs.remove", "fs.rename", "fs.stat",
		"env.get", "env.set", "env.list", "env.unset", "env.checkBinary", "env.which", "env.home", "env.cwd",
		"plugin.check", "plugin.permissions.check", "plugin.permissions.grant", "plugin.permissions.revoke",
		"plugin.config.get", "plugin.config.set", "plugin.cache.get", "plugin.cache.set", "plugin.cache.clear", "plugin.history",
		"system.info", "node.info", "node.list",
	}
	for _, c := range caps {
		cs := r.CheckCapability(c)
		if cs.Level == SupportUnknown {
			t.Errorf("declared capability %q should not be unknown on desktop", c)
		}
		if cs.Capability != c {
			t.Errorf("capability name mismatch: got %q, want %q", cs.Capability, c)
		}
	}
}

func TestSupportDefaultFull(t *testing.T) {
	tests := []struct {
		cap string
		os  platform.RuntimePlatform
	}{
		{"fs.read", desktop()},
		{"fs.read", linux()},
		{"fs.read", darwin()},
		{"session.create", desktop()},
		{"env.get", linux()},
		{"plugin.history", darwin()},
	}
	for _, tt := range tests {
		r := Resolver{Platform: tt.os}
		cs := r.CheckCapability(tt.cap)
		if cs.Level != SupportFull {
			t.Errorf("%s on %s: got %s, want %s", tt.cap, tt.os.OS, cs.Level, SupportFull)
		}
		if !cs.Supported {
			t.Errorf("%s on %s: Supported should be true for full", tt.cap, tt.os.OS)
		}
	}
}

func TestSupportUnknownPlatform(t *testing.T) {
	r := Resolver{Platform: unknownPlat()}
	cs := r.CheckCapability("fs.read")
	if cs.Level == SupportFull {
		t.Error("fs.read on unknown platform should not be full")
	}
}

func TestProcessSpawnPlatforms(t *testing.T) {
	tests := []struct {
		plat    platform.RuntimePlatform
		want    SupportLevel
		wantSup bool
	}{
		{desktop(), SupportPartial, true},
		{linux(), SupportFull, true},
		{darwin(), SupportFull, true},
		{mobile(), SupportUnsupported, false},
		{unknownPlat(), SupportUnsupported, false},
	}
	for _, tt := range tests {
		r := Resolver{Platform: tt.plat}
		cs := r.CheckCapability("process.spawn")
		if cs.Level != tt.want {
			t.Errorf("process.spawn on %s/%s: got %s, want %s", tt.plat.OS, tt.plat.Runtime, cs.Level, tt.want)
		}
		if cs.Supported != tt.wantSup {
			t.Errorf("process.spawn on %s/%s: Supported = %v, want %v", tt.plat.OS, tt.plat.Runtime, cs.Supported, tt.wantSup)
		}
	}

	// Windows specific detail check
	r := Resolver{Platform: desktop()}
	cs := r.CheckCapability("process.spawn")
	if cs.Reason != "pipe_fallback" {
		t.Errorf("process.spawn on windows: Reason = %q, want pipe_fallback", cs.Reason)
	}
	if cs.Detail == "" {
		t.Error("process.spawn on windows: Detail should not be empty")
	}

	// Darwin detail check
	rDarwin := Resolver{Platform: darwin()}
	csDarwin := rDarwin.CheckCapability("process.spawn")
	if csDarwin.Detail != "sandbox_tcc" {
		t.Errorf("process.spawn on darwin: Detail = %q, want sandbox_tcc", csDarwin.Detail)
	}
}

func TestProcessResizePlatforms(t *testing.T) {
	rWin := Resolver{Platform: desktop()}
	cs := rWin.CheckCapability("process.resize")
	if cs.Level != SupportUnsupported {
		t.Errorf("process.resize on windows: got %s, want %s", cs.Level, SupportUnsupported)
	}
	if cs.Reason != "no_pty_resize" {
		t.Errorf("process.resize on windows: Reason = %q, want no_pty_resize", cs.Reason)
	}

	rLinux := Resolver{Platform: linux()}
	csLinux := rLinux.CheckCapability("process.resize")
	if csLinux.Level != SupportFull {
		t.Errorf("process.resize on linux: got %s, want %s", csLinux.Level, SupportFull)
	}
}

func TestProcessSignalPlatforms(t *testing.T) {
	rWin := Resolver{Platform: desktop()}
	cs := rWin.CheckCapability("process.signal")
	if cs.Level != SupportPartial {
		t.Errorf("process.signal on windows: got %s, want %s", cs.Level, SupportPartial)
	}
	if cs.Reason != "limited_signals" {
		t.Errorf("process.signal on windows: Reason = %q, want limited_signals", cs.Reason)
	}

	rLinux := Resolver{Platform: linux()}
	csLinux := rLinux.CheckCapability("process.signal")
	if csLinux.Level != SupportFull {
		t.Errorf("process.signal on linux: got %s, want %s", csLinux.Level, SupportFull)
	}
}

func TestMobileProcessBlocked(t *testing.T) {
	r := Resolver{Platform: mobile()}
	processCaps := []string{"process.spawn", "process.signal", "process.resize", "process.list"}
	for _, c := range processCaps {
		cs := r.CheckCapability(c)
		if cs.Level != SupportUnsupported {
			t.Errorf("%s on mobile: got %s, want %s", c, cs.Level, SupportUnsupported)
		}
		if cs.Reason != "mobile_restricted" {
			t.Errorf("%s on mobile: Reason = %q, want mobile_restricted", c, cs.Reason)
		}
	}
}

func TestMobileFSBlocked(t *testing.T) {
	r := Resolver{Platform: mobile()}
	fsCaps := []string{"fs.read", "fs.write", "fs.list", "fs.mkdir", "fs.remove", "fs.rename", "fs.stat"}
	for _, c := range fsCaps {
		cs := r.CheckCapability(c)
		if cs.Level != SupportUnsupported {
			t.Errorf("%s on mobile: got %s, want %s", c, cs.Level, SupportUnsupported)
		}
	}
}

func TestMobileEnvBlocked(t *testing.T) {
	r := Resolver{Platform: mobile()}
	envCaps := []string{"env.get", "env.set", "env.list", "env.unset", "env.checkBinary", "env.which", "env.home", "env.cwd"}
	for _, c := range envCaps {
		cs := r.CheckCapability(c)
		if cs.Level != SupportUnsupported {
			t.Errorf("%s on mobile: got %s, want %s", c, cs.Level, SupportUnsupported)
		}
	}
}

func TestMobileStreamAllowed(t *testing.T) {
	r := Resolver{Platform: mobile()}

	cs := r.CheckCapability("stream.subscribe")
	if cs.Level != SupportFull {
		t.Errorf("stream.subscribe on mobile: got %s, want %s", cs.Level, SupportFull)
	}

	cs = r.CheckCapability("stream.replay")
	if cs.Level != SupportFull {
		t.Errorf("stream.replay on mobile: got %s, want %s", cs.Level, SupportFull)
	}
}

func TestResolverCheckMany(t *testing.T) {
	r := Resolver{Platform: linux()}
	caps := []string{"process.spawn", "process.resize", "fs.read", "env.get"}
	results := r.CheckMany(caps)
	if len(results) != len(caps) {
		t.Fatalf("CheckMany returned %d results, want %d", len(results), len(caps))
	}
	for i, cs := range results {
		if cs.Capability != caps[i] {
			t.Errorf("result[%d].Capability = %q, want %q", i, cs.Capability, caps[i])
		}
		if cs.Platform.OS != "linux" {
			t.Errorf("result[%d].Platform.OS = %q, want linux", i, cs.Platform.OS)
		}
	}
}

func TestResolverCheckUnknownCap(t *testing.T) {
	r := Resolver{Platform: linux()}
	cs := r.CheckCapability("nonexistent.capability")
	if cs.Level != SupportFull {
		// Unknown capabilities default to full on desktop
		t.Errorf("unknown cap on desktop: got %s, want %s", cs.Level, SupportFull)
	}

	rUnk := Resolver{Platform: unknownPlat()}
	csUnk := rUnk.CheckCapability("nonexistent.capability")
	if csUnk.Level != SupportUnknown {
		t.Errorf("unknown cap on unknown platform: got %s, want %s", csUnk.Level, SupportUnknown)
	}
	if csUnk.Reason != "unknown_capability" {
		t.Errorf("unknown cap on unknown platform: Reason = %q, want unknown_capability", csUnk.Reason)
	}
}

func TestResolverPlatformInResult(t *testing.T) {
	r := Resolver{Platform: darwin()}
	cs := r.CheckCapability("fs.stat")
	if cs.Platform.OS != "darwin" {
		t.Errorf("Platform.OS = %q, want darwin", cs.Platform.OS)
	}
	if cs.Platform.Arch != "arm64" {
		t.Errorf("Platform.Arch = %q, want arm64", cs.Platform.Arch)
	}
	if cs.Platform.Runtime != "desktop" {
		t.Errorf("Platform.Runtime = %q, want desktop", cs.Platform.Runtime)
	}
}

func TestSupportLevelValues(t *testing.T) {
	if SupportFull != "full" {
		t.Errorf("SupportFull = %q, want full", SupportFull)
	}
	if SupportPartial != "partial" {
		t.Errorf("SupportPartial = %q, want partial", SupportPartial)
	}
	if SupportUnsupported != "unsupported" {
		t.Errorf("SupportUnsupported = %q, want unsupported", SupportUnsupported)
	}
	if SupportUnknown != "unknown" {
		t.Errorf("SupportUnknown = %q, want unknown", SupportUnknown)
	}
}

// ── network.* capability tests ─────────────────────────────────────────────

func TestNetworkCapabilitiesDesktopFull(t *testing.T) {
	caps := []string{"network.connect", "network.listen", "network.dns"}
	for _, plat := range []platform.RuntimePlatform{desktop(), linux(), darwin()} {
		r := Resolver{Platform: plat}
		for _, c := range caps {
			cs := r.CheckCapability(c)
			if cs.Level != SupportFull {
				t.Errorf("%s on %s: got %s, want %s", c, plat.OS, cs.Level, SupportFull)
			}
			if !cs.Supported {
				t.Errorf("%s on %s: Supported should be true", c, plat.OS)
			}
		}
	}
}

func TestNetworkCapabilitiesDesktopPartial(t *testing.T) {
	caps := []string{"network.proxy", "network.fetch"}
	for _, plat := range []platform.RuntimePlatform{desktop(), linux(), darwin()} {
		r := Resolver{Platform: plat}
		for _, c := range caps {
			cs := r.CheckCapability(c)
			if cs.Level != SupportPartial {
				t.Errorf("%s on %s: got %s, want %s", c, plat.OS, cs.Level, SupportPartial)
			}
			if !cs.Supported {
				t.Errorf("%s on %s: Supported should be true for partial", c, plat.OS)
			}
			if cs.Reason != "not_implemented" {
				t.Errorf("%s on %s: Reason = %q, want not_implemented", c, plat.OS, cs.Reason)
			}
		}
	}
}

func TestNetworkCapabilitiesMobileBlocked(t *testing.T) {
	r := Resolver{Platform: mobile()}
	networkCaps := []string{"network.connect", "network.listen", "network.dns", "network.proxy", "network.fetch"}
	for _, c := range networkCaps {
		cs := r.CheckCapability(c)
		if cs.Level != SupportUnsupported {
			t.Errorf("%s on mobile: got %s, want %s", c, cs.Level, SupportUnsupported)
		}
		if cs.Reason != "mobile_restricted" {
			t.Errorf("%s on mobile: Reason = %q, want mobile_restricted", c, cs.Reason)
		}
	}
}

func TestNetworkCapabilitiesUnknownPlatform(t *testing.T) {
	r := Resolver{Platform: unknownPlat()}
	for _, c := range []string{"network.connect", "network.proxy"} {
		cs := r.CheckCapability(c)
		if cs.Level == SupportFull {
			t.Errorf("%s on unknown platform should not be full", c)
		}
	}
}
