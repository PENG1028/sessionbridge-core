package update

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func (m *Manager) sourcePath() string { return filepath.Join(m.dataDir, "update-source.json") }
func (m *Manager) policyPath() string { return filepath.Join(m.dataDir, "update-policy.json") }
func (m *Manager) statusPath() string { return filepath.Join(m.dataDir, "update-status.json") }

// persistJSON writes v to path atomically (tmp + rename).
func persistJSON(path string, v interface{}) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// loadJSON reads a JSON file into v. Returns nil if file does not exist.
func loadJSON(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("unmarshal %s: %w", filepath.Base(path), err)
	}
	return nil
}

func (m *Manager) loadSource() error {
	var s UpdateSource
	if err := loadJSON(m.sourcePath(), &s); err != nil {
		return err
	}
	if s.Type != "" {
		m.source = s
	}
	return nil
}

func (m *Manager) loadPolicy() error {
	var p UpdatePolicy
	if err := loadJSON(m.policyPath(), &p); err != nil {
		return err
	}
	// CheckIntervalSeconds of 0 means "not set" — use default
	if p.CheckIntervalSeconds > 0 || p.AutoCheck {
		m.policy = p
	}
	return nil
}

func (m *Manager) loadStatus() error {
	var s UpdateStatus
	if err := loadJSON(m.statusPath(), &s); err != nil {
		return err
	}
	if s.Status != "" {
		m.status = s
	}
	return nil
}
