package oplog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/PENG1028/sessionbridge-core/pkg/types"
)

// ConsistencyReport describes the result of checking a single artifact.
type ConsistencyReport struct {
	OpID         types.OpID `json:"opId"`
	Path         string     `json:"path"`
	Status       string     `json:"status"`       // "ok" | "missing" | "corrupted" | "unchecked"
	ExpectedSize int64      `json:"expectedSize,omitempty"`
	ActualSize   int64      `json:"actualSize,omitempty"`
	ErrorMessage string     `json:"errorMessage,omitempty"`
}

// VerifyArtifacts checks all B-class artifact operations to verify the
// referenced files still exist and (optionally) match their recorded hash.
func (e *RollbackEngine) VerifyArtifacts() []ConsistencyReport {
	ops := e.OpLog.Query(types.OpFilter{
		Classes: []types.OpClass{types.OpClassArtifact},
	})
	if len(ops) == 0 {
		return nil
	}

	var reports []ConsistencyReport
	for _, op := range ops {
		for _, art := range op.Artifacts {
			report := ConsistencyReport{
				OpID:         op.OpID,
				Path:         art.Path,
				ExpectedSize: art.Size,
			}

			info, err := os.Stat(art.Path)
			if os.IsNotExist(err) {
				report.Status = "missing"
				report.ErrorMessage = "file no longer exists"
			} else if err != nil {
				report.Status = "unchecked"
				report.ErrorMessage = fmt.Sprintf("stat error: %v", err)
			} else {
				report.ActualSize = info.Size()
				if art.Hash != "" && quickHashFile(art.Path) != art.Hash {
					report.Status = "corrupted"
					report.ErrorMessage = "content hash mismatch"
				} else {
					report.Status = "ok"
				}
			}
			reports = append(reports, report)
		}
	}
	return reports
}

// VerifyContent verifies all A-class content operations to check if the
// stored ContentStore blob still exists for each before-content reference.
func (e *RollbackEngine) VerifyContent() []ConsistencyReport {
	ops := e.OpLog.Query(types.OpFilter{
		Classes: []types.OpClass{types.OpClassContent},
	})
	if len(ops) == 0 {
		return nil
	}

	var reports []ConsistencyReport
	for _, op := range ops {
		if op.ContentBefore == nil {
			continue
		}
		report := ConsistencyReport{
			OpID:         op.OpID,
			Path:         op.ContentBefore.Path,
			ExpectedSize: op.ContentBefore.Size,
		}

		if _, err := e.Content.Restore(op.ContentBefore); err != nil {
			report.Status = "missing"
			report.ErrorMessage = fmt.Sprintf("content blob not found: %v", err)
		} else {
			report.Status = "ok"
		}
		reports = append(reports, report)
	}
	return reports
}

// quickHashFile computes the SHA256 hex of a file's content.
func quickHashFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
