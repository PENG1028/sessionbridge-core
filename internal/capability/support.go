package capability

import "github.com/user/sessionnode/go-core/internal/platform"

// SupportLevel describes the degree of support for a capability on a platform.
type SupportLevel string

const (
	SupportFull        SupportLevel = "full"
	SupportPartial     SupportLevel = "partial"
	SupportUnsupported SupportLevel = "unsupported"
	SupportUnknown     SupportLevel = "unknown"
)

// CapabilitySupport is the result of checking a capability against a platform.
type CapabilitySupport struct {
	Capability string                   `json:"capability"`
	Supported  bool                     `json:"supported"`
	Level      SupportLevel             `json:"level"`
	Reason     string                   `json:"reason"`
	Detail     string                   `json:"detail,omitempty"`
	Platform   platform.RuntimePlatform `json:"platform"`
}

// platformRule describes the support rules for a capability on a specific OS.
type platformRule struct {
	Level  SupportLevel
	Reason string
	Detail string
}

// Resolver checks capability support against a given platform.
// The Platform field is intentionally a value (not a pointer) so tests can
// inject any platform without calling platform.Current().
type Resolver struct {
	Platform platform.RuntimePlatform
}

// Matrix maps capability to a per-OS rule set.
// Only capabilities with platform-specific behaviour need entries.
// Capabilities NOT in this map default to "full" on desktop, "unsupported" on mobile.
var Matrix = map[string]map[string]platformRule{
	// ── session family ──
	"session.create":  desktopFull,
	"session.destroy": desktopFull,
	"session.list":    desktopFull,
	"session.info":    desktopFull,
	"session.get":     desktopFull,
	"session.stop":    desktopFull,

	// ── stream family ──
	"stream.subscribe": desktopFull,
	"stream.write":     desktopFull,
	"stream.list":      desktopFull,
	"stream.replay":    desktopFull,
	"stream.tail":      desktopFull,

	// ── process family (platform-specific) ──
	"process.spawn": {
		"windows": {SupportPartial, "pipe_fallback", "PTY unavailable on Windows without ConPTY"},
		"linux":   {SupportFull, "", ""},
		"darwin":  {SupportFull, "", "sandbox_tcc"},
	},
	"process.signal": {
		"windows": {SupportPartial, "limited_signals", "Windows only supports kill and interrupt signals"},
		"linux":   {SupportFull, "", ""},
		"darwin":  {SupportFull, "", ""},
	},
	"process.resize": {
		"windows": {SupportUnsupported, "no_pty_resize", ""},
		"linux":   {SupportFull, "", ""},
		"darwin":  {SupportFull, "", ""},
	},
	"process.list": desktopFull,

	// ── fs family ──
	"fs.read":   desktopFull,
	"fs.write":  desktopFull,
	"fs.list":   desktopFull,
	"fs.mkdir":  desktopFull,
	"fs.remove": desktopFull,
	"fs.rename": desktopFull,
	"fs.stat":   desktopFull,

	// ── env family ──
	"env.get":         desktopFull,
	"env.set":         desktopFull,
	"env.list":        desktopFull,
	"env.unset":       desktopFull,
	"env.checkBinary": desktopFull,
	"env.which":       desktopFull,
	"env.home":        desktopFull,
	"env.cwd":         desktopFull,

	// ── plugin family ──
	"plugin.check":              desktopFull,
	"plugin.permissions.check":  desktopFull,
	"plugin.permissions.grant":  desktopFull,
	"plugin.permissions.revoke": desktopFull,
	"plugin.config.get":         desktopFull,
	"plugin.config.set":         desktopFull,
	"plugin.cache.get":          desktopFull,
	"plugin.cache.set":          desktopFull,
	"plugin.cache.clear":        desktopFull,
	"plugin.history":            desktopFull,

	// ── run family ──
	"run.create":       desktopFull,
	"run.list":         desktopFull,
	"run.info":         desktopFull,
	"run.stop":         desktopFull,
	"run.updatePolicy": desktopFull,

	// ── system family ──
	"system.info":  desktopFull,
	"node.info":    desktopFull,
	"node.list":    desktopFull,
	"config.get":   desktopFull,
	"config.list":  desktopFull,
	"config.set":   desktopFull,
	"config.reset": desktopFull,

	// ── network family ──
	"network.connect": desktopFull,
	"network.listen":  desktopFull,
	"network.dns":     desktopFull,
	"network.proxy": {
		"windows": {SupportPartial, "not_implemented", "Network proxy/sandbox not implemented yet"},
		"linux":   {SupportPartial, "not_implemented", "Network proxy/sandbox not implemented yet"},
		"darwin":  {SupportPartial, "not_implemented", "Network proxy/sandbox not implemented yet"},
	},
	"network.fetch": {
		"windows": {SupportPartial, "not_implemented", "Core-managed HTTP fetch not implemented yet"},
		"linux":   {SupportPartial, "not_implemented", "Core-managed HTTP fetch not implemented yet"},
		"darwin":  {SupportPartial, "not_implemented", "Core-managed HTTP fetch not implemented yet"},
	},

	// ── peer management family ──
	"node.peer.list":          desktopFull,
	"node.peer.info":          desktopFull,
	"node.peer.reconnect":     desktopFull,
	"node.peer.disconnect":    desktopFull,
	"node.peer.revoke":        desktopFull,
	"node.reachability.check": desktopFull,

	// ── node identity & invite family ──
	"node.identity.get":  desktopFull,
	"node.invite.create": desktopFull,
	"node.invite.list":   desktopFull,
	"node.invite.revoke": desktopFull,
	"node.invite.accept": desktopFull,
}

// desktopFull is a convenience value for capabilities that are fully supported on all desktop platforms.
var desktopFull = map[string]platformRule{
	"windows": {SupportFull, "", ""},
	"linux":   {SupportFull, "", ""},
	"darwin":  {SupportFull, "", ""},
}

// mobileBlocked lists capability families that are unsupported on mobile/browser.
var mobileBlocked = []string{"process.", "fs.", "env.", "network."}

// CheckCapability returns the support level for a single capability on the resolver's platform.
func (r Resolver) CheckCapability(capability string) CapabilitySupport {
	cs := CapabilitySupport{
		Capability: capability,
		Platform:   r.Platform,
	}

	// ── Mobile / browser blanket rule ──
	if r.Platform.IsMobile() || r.Platform.Runtime == "browser" {
		for _, prefix := range mobileBlocked {
			if hasPrefix(capability, prefix) {
				cs.Supported = false
				cs.Level = SupportUnsupported
				cs.Reason = "mobile_restricted"
				cs.Detail = "Capability family " + prefix + " is not available on mobile/browser platforms"
				return cs
			}
		}
		// stream subscribe/replay explicitly full on mobile
		if capability == "stream.subscribe" || capability == "stream.replay" {
			cs.Supported = true
			cs.Level = SupportFull
			return cs
		}
		// Other capabilities on mobile: check Matrix or default to unsupported
		if rules, ok := Matrix[capability]; ok {
			if rule, ok := rules[r.Platform.OS]; ok {
				return buildSupport(cs, rule)
			}
		}
		cs.Supported = false
		cs.Level = SupportUnsupported
		cs.Reason = "mobile_restricted"
		return cs
	}

	// ── Desktop / server ──
	if rules, ok := Matrix[capability]; ok {
		if rule, ok := rules[r.Platform.OS]; ok {
			return buildSupport(cs, rule)
		}
		// Capability is in Matrix but no rule for this OS.
		// On desktop/server it defaults to full; on other platforms it is unsupported.
		if r.Platform.IsDesktop() || r.Platform.Runtime == "server" {
			cs.Supported = true
			cs.Level = SupportFull
		} else {
			cs.Supported = false
			cs.Level = SupportUnsupported
			cs.Reason = "platform_not_listed"
		}
		return cs
	}

	// ── Unknown capability (not in Matrix) ──
	if r.Platform.IsDesktop() || r.Platform.Runtime == "server" {
		cs.Supported = true
		cs.Level = SupportFull
		return cs
	}

	cs.Supported = false
	cs.Level = SupportUnknown
	cs.Reason = "unknown_capability"
	return cs
}

// CheckMany returns support results for all given capabilities.
func (r Resolver) CheckMany(capabilities []string) []CapabilitySupport {
	results := make([]CapabilitySupport, 0, len(capabilities))
	for _, c := range capabilities {
		results = append(results, r.CheckCapability(c))
	}
	return results
}

func buildSupport(cs CapabilitySupport, rule platformRule) CapabilitySupport {
	cs.Level = rule.Level
	cs.Reason = rule.Reason
	cs.Detail = rule.Detail
	cs.Supported = rule.Level == SupportFull || rule.Level == SupportPartial
	return cs
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
