package capability

import (
	"testing"

	"github.com/user/sessionnode/go-core/internal/platform"
)

func TestSupportDeclared(t *testing.T) {
	if Support("process.spawn", platform.Windows) != Partial {
		t.Error("process.spawn on windows should be Partial")
	}
	if Support("process.spawn", platform.Linux) != Full {
		t.Error("process.spawn on linux should be Full")
	}
	if Support("process.resize", platform.Windows) != Unsupported {
		t.Error("process.resize on windows should be Unsupported")
	}
}

func TestSupportDefaultFull(t *testing.T) {
	if Support("fs.read", platform.Windows) != Full {
		t.Error("fs.read on windows should be Full (default)")
	}
	if Support("fs.read", platform.Linux) != Full {
		t.Error("fs.read on linux should be Full (default)")
	}
}

func TestSupportUnknownPlatform(t *testing.T) {
	if Support("fs.read", platform.Unknown) != Unsupported {
		t.Error("fs.read on unknown should be Unsupported")
	}
}
