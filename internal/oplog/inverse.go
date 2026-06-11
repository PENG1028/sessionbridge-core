package oplog

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/PENG1028/sessionbridge-core/pkg/types"
)

// InverseFunc is a function that undoes an operation.
// It receives the original operation and returns an error if the rollback fails.
type InverseFunc func(op *types.Operation, engine *RollbackEngine) error

// inverseOps maps capabilities to their inverse functions.
// Populated via RegisterInverse at init time.
var inverseOps = map[string]InverseFunc{
	// A-class: content restore
	"fs.write":   inverseFsWrite,
	"config.set": inverseConfigSet,
	"env.set":    inverseEnvSet,

	// B-class: delete artifacts
	"fs.remove": inverseFsRemove,
	"fs.mkdir":  inverseFsMkdir,
	"download":  inverseDownload,
	"install":   inverseInstall,

	// C-class: state reversal
	"session.create":  inverseSessionCreate,
	"session.destroy": inverseSessionDestroy,
	"run.create":      inverseRunCreate,
	"run.stop":        inverseRunStop,
	"process.spawn":   inverseProcessSpawn,
	"peer.connect":    inversePeerConnect,
}

// RegisterInverse registers an inverse function for the given capability.
// Used by tests and plugin-specific inverses.
func RegisterInverse(capability string, fn InverseFunc) {
	inverseOps[capability] = fn
}

// --- A-class inverses: content restore ---

// inverseFsWrite restores the content that was overwritten.
func inverseFsWrite(op *types.Operation, engine *RollbackEngine) error {
	if op.ContentBefore == nil {
		return fmt.Errorf("no content backup for fs.write rollback (op %s)", op.OpID)
	}
	data, err := engine.Content.Restore(op.ContentBefore)
	if err != nil {
		return fmt.Errorf("restore content: %w", err)
	}
	if err := os.WriteFile(op.ContentBefore.Path, data, 0644); err != nil {
		return fmt.Errorf("write restored file: %w", err)
	}
	return nil
}

// inverseConfigSet restores the previous config value (stub).
func inverseConfigSet(op *types.Operation, engine *RollbackEngine) error {
	return fmt.Errorf("config.set rollback not yet implemented")
}

// inverseEnvSet restores the previous env value (stub).
func inverseEnvSet(op *types.Operation, engine *RollbackEngine) error {
	return fmt.Errorf("env.set rollback not yet implemented")
}

// --- B-class inverses: artifact deletion ---

// inverseFsRemove restores a removed file (requires trash backup, stub).
func inverseFsRemove(op *types.Operation, engine *RollbackEngine) error {
	return fmt.Errorf("fs.remove rollback not yet implemented (needs trash backup)")
}

// inverseFsMkdir removes a created directory.
func inverseFsMkdir(op *types.Operation, engine *RollbackEngine) error {
	if len(op.Params) == 0 {
		return fmt.Errorf("no params for fs.mkdir rollback")
	}
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(op.Params, &params); err != nil {
		return fmt.Errorf("unmarshal fs.mkdir params: %w", err)
	}
	if params.Path != "" {
		return os.RemoveAll(params.Path)
	}
	return nil
}

// inverseDownload removes a downloaded file.
func inverseDownload(op *types.Operation, engine *RollbackEngine) error {
	for _, art := range op.Artifacts {
		if err := os.Remove(art.Path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove download artifact %s: %w", art.Path, err)
		}
	}
	return nil
}

// inverseInstall uninstalls a package (stub — plugin-specific).
func inverseInstall(op *types.Operation, engine *RollbackEngine) error {
	return fmt.Errorf("install rollback not yet implemented (plugin-specific)")
}

// --- C-class inverses: state reversal ---

// inverseSessionCreate destroys the created session (stub, needs dispatcher).
func inverseSessionCreate(op *types.Operation, engine *RollbackEngine) error {
	return fmt.Errorf("session.create rollback requires dispatcher call")
}

// inverseSessionDestroy recreates the destroyed session (stub).
func inverseSessionDestroy(op *types.Operation, engine *RollbackEngine) error {
	return fmt.Errorf("session.destroy rollback not yet implemented")
}

// inverseRunCreate stops the created run (stub).
func inverseRunCreate(op *types.Operation, engine *RollbackEngine) error {
	return fmt.Errorf("run.create rollback not yet implemented")
}

// inverseRunStop restarts the stopped run (stub).
func inverseRunStop(op *types.Operation, engine *RollbackEngine) error {
	return fmt.Errorf("run.stop rollback not yet implemented")
}

// inverseProcessSpawn sends SIGTERM to the spawned process (stub, needs dispatcher).
func inverseProcessSpawn(op *types.Operation, engine *RollbackEngine) error {
	return fmt.Errorf("process.spawn rollback requires dispatcher call")
}

// inversePeerConnect disconnects a connected peer (stub).
func inversePeerConnect(op *types.Operation, engine *RollbackEngine) error {
	return fmt.Errorf("peer.connect rollback not yet implemented")
}
