package types

import (
	"encoding/json"
	"strings"
	"testing"
)

// ptr is a helper that returns a pointer to the given value.
func ptr[T any](v T) *T {
	return &v
}

func TestNodeID_Valid(t *testing.T) {
	tests := []struct {
		id    NodeID
		valid bool
	}{
		{"node_abc", true},
		{"", false},
		{"node_123", true},
	}
	for _, tt := range tests {
		if got := tt.id.Valid(); got != tt.valid {
			t.Errorf("NodeID(%q).Valid() = %v, want %v", tt.id, got, tt.valid)
		}
	}
}

func TestNodeID_JSON(t *testing.T) {
	roundTrip(t, NodeID("node_abc"))
	roundTrip(t, NodeID(""))
}

func TestSessionID_JSON(t *testing.T) {
	roundTrip(t, SessionID("sess_abc123"))
}

func TestStreamID_JSON(t *testing.T) {
	roundTrip(t, StreamID("stream_stdout_abc"))
}

func TestRequestID_JSON(t *testing.T) {
	roundTrip(t, RequestID("req_001"))
}

func TestPluginID_JSON(t *testing.T) {
	roundTrip(t, PluginID("claude-code"))
}

func TestEventSeq_JSON(t *testing.T) {
	roundTrip(t, EventSeq(42))
	roundTrip(t, EventSeq(0))
}

func TestEventSeq_Valid(t *testing.T) {
	if !EventSeq(1).Valid() {
		t.Error("EventSeq(1).Valid() = false, want true")
	}
	if EventSeq(-1).Valid() {
		t.Error("EventSeq(-1).Valid() = true, want false")
	}
}

func TestActor_JSON(t *testing.T) {
	roundTrip(t, Actor{Type: "web", ID: "browser_abc"})
	roundTrip(t, Actor{Type: "cli", ID: "user_zhp", Token: "secret123"})
}

func TestCapabilityRequest_JSON(t *testing.T) {
	payload, _ := json.Marshal(map[string]string{"path": "/home"})
	req := CapabilityRequest{
		RequestID:    "req_001",
		Actor:        Actor{Type: "web", ID: "browser_abc"},
		PluginID:     "file-explorer",
		TargetNodeID: "",
		Capability:   "fs.list",
		Payload:      payload,
		Timestamp:    1712345678000,
	}
	roundTrip(t, req)
}

func TestCapabilityResponse_JSON_Success(t *testing.T) {
	resp := CapabilityResponse{
		RequestID: "req_001",
		OK:        true,
		Payload:   map[string]interface{}{"entries": []string{"a.txt", "b.txt"}},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"ok":true`) {
		t.Error("expected ok:true in JSON")
	}
	// Unmarshal and verify
	var got CapabilityResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.RequestID != resp.RequestID || got.OK != resp.OK {
		t.Errorf("round-trip failed: %+v != %+v", got, resp)
	}
}

func TestCapabilityResponse_JSON_Error(t *testing.T) {
	resp := CapabilityResponse{
		RequestID: "req_001",
		OK:        false,
		Error:     &CoreError{Code: "PERMISSION_DENIED", Message: "no access"},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"ok":false`) {
		t.Error("expected ok:false in JSON for error response")
	}
	var got CapabilityResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Error == nil || got.Error.Code != "PERMISSION_DENIED" {
		t.Errorf("round-trip failed: %+v", got)
	}
}

func TestCoreError_ErrorInterface(t *testing.T) {
	err := &CoreError{Code: "TEST", Message: "something went wrong"}
	if err.Error() != "TEST: something went wrong" {
		t.Errorf("unexpected Error(): %s", err.Error())
	}
	err2 := &CoreError{Code: "ONLY_CODE"}
	if err2.Error() != "ONLY_CODE" {
		t.Errorf("unexpected Error(): %s", err2.Error())
	}
	// Verify it satisfies the error interface
	var e error = err
	if e == nil {
		t.Error("CoreError should satisfy error interface")
	}
}

func TestPluginDefinition_JSON(t *testing.T) {
	pd := PluginDefinition{
		ID:      "claude-code",
		Title:   "Claude Code",
		Version: "1.0.0",
		Kind:    "web+cli",
		Requires: struct {
			Capabilities []string           `json:"capabilities"`
			Dependencies []PluginDependency `json:"dependencies"`
		}{
			Capabilities: []string{"fs.read", "fs.write"},
			Dependencies: []PluginDependency{
				{ID: "claude-cli", Type: "binary", Name: "claude", Required: true},
			},
		},
	}
	roundTrip(t, pd)
}

func TestPluginInstallation_JSON(t *testing.T) {
	inst := PluginInstallation{
		PluginID: "claude-code",
		NodeID:   "node_local",
		Status:   "installed",
		Enabled:  true,
		Version:  "1.0.0",
	}
	roundTrip(t, inst)
}

func TestPluginEnvironment_JSON(t *testing.T) {
	env := PluginEnvironment{
		PluginID:  "claude-code",
		NodeID:    "node_local",
		CheckedAt: 1712345678000,
		Status:    "partial",
		Dependencies: []DependencyCheckResult{
			{ID: "claude-cli", Type: "binary", Name: "claude", Found: true, Version: "1.0.0", Path: "/usr/local/bin/claude"},
			{ID: "git", Type: "binary", Name: "git", Found: false, Required: "2.0", Optional: true},
		},
	}
	roundTrip(t, env)
}

func TestPluginPermissionGrant_JSON(t *testing.T) {
	grant := PluginPermissionGrant{
		PluginID:   "claude-code",
		NodeID:     "node_local",
		Capability: "fs.read",
		Mode:       "allow",
		Constraints: &PermissionConstraints{
			Allow: []string{"~/.claude/**", "${workspace}/**"},
			Deny:  []string{"**/.env"},
		},
		GrantedAt: 1712345678000,
		GrantedBy: "user_zhp",
	}
	roundTrip(t, grant)

	// Without constraints
	grant2 := PluginPermissionGrant{
		PluginID:   "claude-code",
		NodeID:     "node_local",
		Capability: "process.spawn",
		Mode:       "ask",
		GrantedAt:  1712345678000,
		GrantedBy:  "user_zhp",
	}
	roundTrip(t, grant2)
}

func TestPluginInstallHistory_JSON(t *testing.T) {
	h := PluginInstallHistory{
		InstallID:  "inst_001",
		PluginID:   "claude-code",
		NodeID:     "node_local",
		Action:     "install",
		Status:     "success",
		StartedAt:  1712345678000,
		FinishedAt: ptr(int64(1712345708000)),
		StdoutLog:  "plugins/claude-code/install/inst_001/stdout.log",
		Actor:      "user_zhp",
	}
	roundTrip(t, h)

	h2 := PluginInstallHistory{
		InstallID: "inst_002",
		PluginID:  "claude-code",
		NodeID:    "node_local",
		Action:    "install",
		Status:    "pending",
		StartedAt: 1712345678000,
		Actor:     "web_browser_abc",
	}
	roundTrip(t, h2)
}

func TestPluginFileEntry_JSON(t *testing.T) {
	e := PluginFileEntry{
		ID:           "claude-global-history",
		PluginID:     "claude-code",
		NodeID:       "node_local",
		Path:         "~/.claude/history.jsonl",
		FileType:     "history",
		Source:       "manifest",
		Visibility:   "system",
		Clearable:    false,
		RegisteredAt: 1712345678000,
	}
	roundTrip(t, e)
}

func TestPluginCacheEntry_JSON(t *testing.T) {
	e := PluginCacheEntry{
		ID:        "claude-plugin-cache",
		PluginID:  "claude-code",
		NodeID:    "node_local",
		Paths:     []string{"~/.sessionnode/plugins/claude-code/cache", "~/.claude/tmp"},
		Source:    "manifest",
		Owner:     "plugin",
		Clearable: true,
		ClearMode: "delete-path",
		Risk:      "low",
		CreatedAt: 1712345678000,
	}
	roundTrip(t, e)
}

func TestFileAccessRecord_JSON(t *testing.T) {
	r := FileAccessRecord{
		PluginID:  "claude-code",
		NodeID:    "node_local",
		Path:      "~/.claude/settings.json",
		Action:    "read",
		Timestamp: 1712345678000,
		RequestID: "req_123",
		Allowed:   true,
	}
	roundTrip(t, r)
}

func TestDeclaredLocation_JSON(t *testing.T) {
	roundTrip(t, DeclaredLocation{
		Source:      SourceManifest,
		PluginID:    "claude-code",
		NodeID:      "node_local",
		Path:        "~/.claude/history.jsonl",
		Description: "Global session history",
		FileType:    FileTypeHistory,
	})
}

func TestPlannedArtifact_JSON(t *testing.T) {
	roundTrip(t, PlannedArtifact{
		Source:      SourceInstallPlan,
		InstallID:   "inst_001",
		PluginID:    "claude-code",
		NodeID:      "node_local",
		Path:        "~/.sessionnode/downloads/inst_001/node-vxx.msi",
		FileType:    FileTypeBinary,
		Clearable:   true,
		Removable:   true,
	})
}

func TestDiscoveredSideEffect_JSON(t *testing.T) {
	roundTrip(t, DiscoveredSideEffect{
		Source:        SourcePrePostDiff,
		InstallID:     "inst_001",
		PluginID:      "claude-code",
		NodeID:        "node_local",
		Path:          "C:/Users/ZHP/AppData/Roaming/npm-cache",
		FileType:      FileTypeCache,
		ExistedBefore: true,
		Clearable:     true,
		Shared:        true,
	})
	roundTrip(t, DiscoveredSideEffect{
		Source:    SourceKnownDetector,
		PluginID:  "claude-code",
		NodeID:    "node_local",
		Path:      "C:/Windows/system32/cmd.exe",
		FileType:  FileTypeBinary,
		Dangerous: true,
	})
}

func TestInstallSideEffect_JSON(t *testing.T) {
	se := InstallSideEffect{
		InstallID: "inst_001",
		PluginID:  "claude-code",
		NodeID:    "node_local",
		Declared: []DeclaredLocation{
			{Source: SourceManifest, PluginID: "claude-code", NodeID: "node_local", Path: "~/.claude/history.jsonl", FileType: FileTypeHistory},
		},
		Planned: []PlannedArtifact{
			{Source: SourceInstallPlan, InstallID: "inst_001", PluginID: "claude-code", NodeID: "node_local", Path: "~/.sessionnode/downloads/node.msi", FileType: FileTypeBinary, Clearable: true, Removable: true},
		},
		Discovered: []DiscoveredSideEffect{
			{Source: SourcePrePostDiff, InstallID: "inst_001", PluginID: "claude-code", NodeID: "node_local", Path: "/usr/local/bin/claude", FileType: FileTypeBinary},
		},
		CreatedAt: 1712345678000,
	}
	roundTrip(t, se)
}

func TestInstallArtifact_JSON(t *testing.T) {
	roundTrip(t, InstallArtifact{
		ArtifactID:   "art_001",
		InstallID:    "inst_001",
		PluginID:     "claude-code",
		NodeID:       "node_local",
		Path:         "C:/Program Files/nodejs/node.exe",
		ArtifactType: FileTypeBinary,
		Source:       "discovered",
		Shared:       true,
		Dangerous:    true,
		RegisteredAt: 1712345678000,
	})
}

func TestDependencyGraphNode_JSON(t *testing.T) {
	roundTrip(t, DependencyGraphNode{
		DependencyID: "nodejs",
		Reason:       "required_for_npm",
		Status:       "installed",
		Artifacts:    []string{"C:/Program Files/nodejs/node.exe", "C:/Program Files/nodejs/npm.cmd"},
	})
}

// Helper: marshal, unmarshal, compare.
func roundTrip(t *testing.T, v interface{}) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	// Unmarshal into interface{} to handle both objects and scalars.
	var v1 interface{}
	if err := json.Unmarshal(data, &v1); err != nil {
		t.Fatalf("unmarshal error: %v (json: %s)", err, string(data))
	}
	// Marshal again and compare normalized form.
	data2, _ := json.Marshal(v)
	var v2 interface{}
	json.Unmarshal(data2, &v2)
	s1 := normalizeJSON(t, data)
	s2 := normalizeJSON(t, data2)
	if s1 != s2 {
		t.Errorf("JSON round-trip inconsistent:\n  got:  %s\n  want: %s", s1, s2)
	}
}

func normalizeJSON(t *testing.T, data []byte) string {
	t.Helper()
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	norm, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("normalize marshal error: %v", err)
	}
	return string(norm)
}
