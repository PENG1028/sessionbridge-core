package capability

import "github.com/user/sessionnode/go-core/internal/platform"

type SupportLevel string

const (
	Full        SupportLevel = "full"
	Partial     SupportLevel = "partial"
	Unsupported SupportLevel = "unsupported"
)

// Matrix maps capability → platform → support level
// Only capabilities with platform-specific behavior need entries.
// Capabilities NOT in this map are assumed "full" on all desktop platforms.
var Matrix = map[string]map[platform.Platform]SupportLevel{
	"process.spawn": {
		platform.Windows: Partial,
		platform.Linux:   Full,
		platform.Darwin:  Full,
		platform.Unknown: Unsupported,
	},
	"process.resize": {
		platform.Windows: Unsupported,
		platform.Linux:   Full,
		platform.Darwin:  Full,
		platform.Unknown: Unsupported,
	},
}

func Support(capability string, plat platform.Platform) SupportLevel {
	if m, ok := Matrix[capability]; ok {
		if s, ok := m[plat]; ok {
			return s
		}
		return Unsupported
	}
	if plat == platform.Linux || plat == platform.Darwin || plat == platform.Windows {
		return Full
	}
	return Unsupported
}
