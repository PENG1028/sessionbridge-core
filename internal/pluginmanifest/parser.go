package pluginmanifest

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Supported manifest versions.
var supportedVersions = map[string]bool{
	"1": true,
}

// LoadFile loads and parses a manifest from a file path.
// Supports both .yaml and .json extensions.
func LoadFile(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest file: %w", err)
	}

	if strings.HasSuffix(path, ".json") {
		return ParseJSON(data)
	}
	return ParseYAML(data)
}

// ParseYAML parses a YAML manifest.
func ParseYAML(data []byte) (*Manifest, error) {
	jsonData, err := yamlToJSON(data)
	if err != nil {
		return nil, fmt.Errorf("yaml to json conversion: %w", err)
	}
	return parseJSONData(jsonData)
}

// ParseJSON parses a JSON manifest.
func ParseJSON(data []byte) (*Manifest, error) {
	return parseJSONData(data)
}

func parseJSONData(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}
	return &m, nil
}

// StringSlice is a helper that returns a string representation of a string slice.
func StringSlice(s []string) string {
	if len(s) == 0 {
		return "[]"
	}
	return "[" + strings.Join(s, ", ") + "]"
}

// IsSupportedManifestVersion returns true if the version string is supported.
func IsSupportedManifestVersion(version string) bool {
	return supportedVersions[version]
}
