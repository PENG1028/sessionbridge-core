package topology

import (
	"testing"

	"github.com/user/sessionnode/go-core/pkg/types"
)

func TestNew_SingleLocal(t *testing.T) {
	pt := New(Config{
		LocalID:   "node-main",
		LocalName: "dev",
	})

	nodes := pt.ListNodes()
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}

	n := nodes[0]
	if n.ID != "node-main" {
		t.Errorf("expected ID node-main, got %s", n.ID)
	}
	if n.Status != StatusLocal {
		t.Errorf("expected Status=local, got %s", n.Status)
	}
	if n.DisplayName != "dev" {
		t.Errorf("expected DisplayName=dev, got %s", n.DisplayName)
	}
	if n.Name != "dev" {
		t.Errorf("expected Name=dev, got %s", n.Name)
	}
}

func TestNew_WithPeers(t *testing.T) {
	pt := New(Config{
		LocalID:   "node-main",
		LocalName: "dev",
		Peers: []PeerConfig{
			{ID: "node-w1", Address: "localhost:9091", Tags: []string{"local"}},
			{ID: "vps-node", Address: "43.160.241.180:8080", Tags: []string{"remote"}},
		},
	})

	nodes := pt.ListNodes()
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes (local + 2 peers), got %d", len(nodes))
	}

	// Build lookup
	byID := make(map[types.NodeID]int)
	for i, n := range nodes {
		byID[n.ID] = i
	}

	// Local node
	local, ok := byID["node-main"]
	if !ok {
		t.Fatal("local node not found in listing")
	}
	if nodes[local].Status != StatusLocal {
		t.Errorf("local status should be local, got %s", nodes[local].Status)
	}

	// Local peer
	w1, ok := byID["node-w1"]
	if !ok {
		t.Fatal("node-w1 not found")
	}
	if nodes[w1].Status != StatusDisconnected {
		t.Errorf("peer status should be disconnected, got %s", nodes[w1].Status)
	}
	if nodes[w1].Address != "localhost:9091" {
		t.Errorf("expected address localhost:9091, got %s", nodes[w1].Address)
	}

	// Remote peer
	vps, ok := byID["vps-node"]
	if !ok {
		t.Fatal("vps-node not found")
	}
	if nodes[vps].Address != "43.160.241.180:8080" {
		t.Errorf("expected address 43.160.241.180:8080, got %s", nodes[vps].Address)
	}
}

func TestGet_Unknown(t *testing.T) {
	pt := New(Config{
		LocalID:   "node-main",
		LocalName: "dev",
	})

	_, err := pt.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown node")
	}
}

func TestGet_Local(t *testing.T) {
	pt := New(Config{
		LocalID:   "node-main",
		LocalName: "dev",
	})

	_, err := pt.Get("node-main")
	if err == nil {
		t.Fatal("expected error when Get is called for local node (should be handled by dispatcher)")
	}
}

func TestListNodes_LocalOnly(t *testing.T) {
	pt := New(Config{
		LocalID:   "main",
		LocalName: "my-machine",
	})

	nodes := pt.ListNodes()
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].DisplayName != "my-machine" {
		t.Errorf("single local display should be name only, got %s", nodes[0].DisplayName)
	}
}

func TestListNodes_MultiLocalDisplay(t *testing.T) {
	pt := New(Config{
		LocalID:   "main",
		LocalName: "dev",
		Peers: []PeerConfig{
			{ID: "node-w1", Address: "localhost:9091", Tags: []string{"local"}},
		},
	})

	nodes := pt.ListNodes()
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}

	byID := make(map[types.NodeID]string)
	for _, n := range nodes {
		byID[n.ID] = n.DisplayName
	}

	// Two local peers → both get numbered
	if byID["main"] != "dev-(1)" {
		t.Errorf("expected main display 'dev-(1)', got %q", byID["main"])
	}
	if byID["node-w1"] != "dev-(2)" {
		t.Errorf("expected node-w1 display 'dev-(2)', got %q", byID["node-w1"])
	}
}

func TestListNodes_ThreeLocal(t *testing.T) {
	pt := New(Config{
		LocalID:   "a",
		LocalName: "dev",
		Peers: []PeerConfig{
			{ID: "b", Address: "localhost:9091", Tags: []string{"local"}},
			{ID: "c", Address: "localhost:9092", Tags: []string{"local"}},
		},
	})

	nodes := pt.ListNodes()
	byID := make(map[types.NodeID]string)
	for _, n := range nodes {
		byID[n.ID] = n.DisplayName
	}

	if byID["a"] != "dev-(1)" {
		t.Errorf("expected a='dev-(1)', got %q", byID["a"])
	}
	if byID["b"] != "dev-(2)" {
		t.Errorf("expected b='dev-(2)', got %q", byID["b"])
	}
	if byID["c"] != "dev-(3)" {
		t.Errorf("expected c='dev-(3)', got %q", byID["c"])
	}
}

func TestListNodes_RemoteDisplay(t *testing.T) {
	pt := New(Config{
		LocalID:   "main",
		LocalName: "dev",
		Peers: []PeerConfig{
			{ID: "vps-node", Address: "43.160.241.180:8080", Tags: []string{"remote"}},
		},
	})

	nodes := pt.ListNodes()
	byID := make(map[types.NodeID]string)
	for _, n := range nodes {
		byID[n.ID] = n.DisplayName
	}

	// Single local → no number
	if byID["main"] != "dev" {
		t.Errorf("expected main='dev', got %q", byID["main"])
	}
	// Remote → uses ID
	if byID["vps-node"] != "vps-node" {
		t.Errorf("expected vps-node='vps-node', got %q", byID["vps-node"])
	}
}

func TestHasTag(t *testing.T) {
	if !hasTag([]string{"local"}, "local") {
		t.Error("expected hasTag to find 'local'")
	}
	if !hasTag([]string{"remote", "local"}, "local") {
		t.Error("expected hasTag to find 'local' in multi-tag")
	}
	if hasTag([]string{"remote"}, "local") {
		t.Error("expected hasTag to not find 'local'")
	}
	if hasTag(nil, "local") {
		t.Error("expected hasTag to return false for nil")
	}
}

func TestComputeLocalDisplayNames_None(t *testing.T) {
	m := computeLocalDisplayNames(nil, "dev")
	if len(m) != 0 {
		t.Errorf("expected empty map, got %d entries", len(m))
	}
}

func TestComputeLocalDisplayNames_Single(t *testing.T) {
	peers := []*Peer{{ID: "main"}}
	m := computeLocalDisplayNames(peers, "dev")
	if m["main"] != "dev" {
		t.Errorf("expected 'dev', got %q", m["main"])
	}
}

func TestComputeLocalDisplayNames_Multi(t *testing.T) {
	peers := []*Peer{
		{ID: "a"},
		{ID: "b"},
		{ID: "c"},
	}
	m := computeLocalDisplayNames(peers, "dev")
	if m["a"] != "dev-(1)" {
		t.Errorf("expected a='dev-(1)', got %q", m["a"])
	}
	if m["b"] != "dev-(2)" {
		t.Errorf("expected b='dev-(2)', got %q", m["b"])
	}
	if m["c"] != "dev-(3)" {
		t.Errorf("expected c='dev-(3)', got %q", m["c"])
	}
}

func TestPeerName_Local(t *testing.T) {
	p := &Peer{ID: "main"}
	name := peerName(p, "main", "my-machine")
	if name != "my-machine" {
		t.Errorf("expected 'my-machine', got %q", name)
	}
}

func TestPeerName_Remote(t *testing.T) {
	p := &Peer{ID: "vps-node"}
	name := peerName(p, "main", "dev")
	if name != "vps-node" {
		t.Errorf("expected 'vps-node', got %q", name)
	}
}
