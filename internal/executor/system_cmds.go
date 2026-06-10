package executor

import (
	"os"
	"runtime"
	"time"

	"github.com/PENG1028/sessionbridge-core/pkg/types"
)

func systemInfo(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	hostname, _ := os.Hostname()
	return map[string]interface{}{
		"hostname":     hostname,
		"os":           runtime.GOOS,
		"arch":         runtime.GOARCH,
		"goVersion":    runtime.Version(),
		"numCPU":       runtime.NumCPU(),
		"numGoroutine": runtime.NumGoroutine(),
		"timestamp":    time.Now().UnixMilli(),
	}, nil
}
