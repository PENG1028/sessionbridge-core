package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/PENG1028/sessionbridge-core/internal/mesh"
)

// TestPeerRegister_Endpoint validates the /peer/register HTTP endpoint shape.
func TestPeerRegister_Endpoint(t *testing.T) {
	identity, err := mesh.LoadOrCreateIdentity(t.TempDir(), "test-register-node")
	if err != nil {
		t.Fatalf("create identity: %v", err)
	}
	ts := mesh.NewTrustStore(filepath.Join(t.TempDir(), "trusted_peers.json"))

	sv := &Server{
		identity:       identity,
		trustStore:     ts,
		registerPolicy: "open",
	}
	sv.SetRegisterPolicy("open", "")
	mux := http.NewServeMux()
	mux.HandleFunc("/peer/register", sv.handlePeerRegister)
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	// ── Test 1: Missing fields returns 400 ──
	t.Run("missing fields", func(t *testing.T) {
		resp, err := http.Post(httpSrv.URL+"/peer/register", "application/json",
			bytes.NewReader([]byte(`{}`)))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Errorf("expected 400, got %d", resp.StatusCode)
		}
		var body map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&body)
		if body["error"] == nil {
			t.Error("expected error message")
		}
		t.Logf("missing fields: %v", body["error"])
	})

	// ── Test 2: Valid registration returns 200 ──
	t.Run("valid registration", func(t *testing.T) {
		peerID, err := mesh.LoadOrCreateIdentity(t.TempDir(), "test-peer")
		if err != nil {
			t.Fatalf("create peer identity: %v", err)
		}
		reqBody, _ := json.Marshal(map[string]string{
			"nodeId":      peerID.NodeID,
			"publicKey":   hexEncode(peerID.PublicKey),
			"fingerprint": peerID.Fingerprint,
			"mode":        "transit",
		})
		resp, err := http.Post(httpSrv.URL+"/peer/register", "application/json",
			bytes.NewReader(reqBody))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		var body map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&body)
		if body["status"] != "registered" {
			t.Errorf("status = %v, want 'registered'", body["status"])
		}
		if body["mode"] != "transit" {
			t.Errorf("mode = %v, want 'transit'", body["mode"])
		}
		if body["nodeId"] == nil {
			t.Error("missing nodeId in response")
		}
		if body["publicKey"] == nil {
			t.Error("missing publicKey in response")
		}
		if body["fingerprint"] == nil {
			t.Error("missing fingerprint in response")
		}
		if body["peerWsPath"] == nil {
			t.Error("missing peerWsPath in response")
		}
		t.Logf("registered: nodeId=%v mode=%v", body["nodeId"], body["mode"])
	})

	// ── Test 3: Self-registration returns 400 ──
	t.Run("self registration rejected", func(t *testing.T) {
		reqBody, _ := json.Marshal(map[string]string{
			"nodeId":      identity.NodeID,
			"publicKey":   hexEncode(identity.PublicKey),
			"fingerprint": identity.Fingerprint,
		})
		resp, err := http.Post(httpSrv.URL+"/peer/register", "application/json",
			bytes.NewReader(reqBody))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Errorf("expected 400, got %d", resp.StatusCode)
		}
	})

	// ── Test 4: Invalid method returns 405 ──
	t.Run("GET rejected", func(t *testing.T) {
		resp, err := http.Get(httpSrv.URL + "/peer/register")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 405 {
			t.Errorf("expected 405, got %d", resp.StatusCode)
		}
	})

	// ── Test 5: Invalid public key returns 400 ──
	t.Run("invalid public key", func(t *testing.T) {
		reqBody, _ := json.Marshal(map[string]string{
			"nodeId":      "bad-peer",
			"publicKey":   "not-a-valid-hex-key",
			"fingerprint": "abc",
		})
		resp, err := http.Post(httpSrv.URL+"/peer/register", "application/json",
			bytes.NewReader(reqBody))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Errorf("expected 400, got %d", resp.StatusCode)
		}
	})
}

// TestPeerRegister_TokenPolicy validates token-based registration policy.
func TestPeerRegister_TokenPolicy(t *testing.T) {
	identity, err := mesh.LoadOrCreateIdentity(t.TempDir(), "token-register-node")
	if err != nil {
		t.Fatalf("create identity: %v", err)
	}
	ts := mesh.NewTrustStore(filepath.Join(t.TempDir(), "trusted_peers.json"))

	sv := &Server{
		identity:       identity,
		trustStore:     ts,
		registerPolicy: "token",
		registerToken:  "secret123",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/peer/register", sv.handlePeerRegister)
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	// Test: missing token
	t.Run("missing token rejected", func(t *testing.T) {
		peerID, _ := mesh.LoadOrCreateIdentity(t.TempDir(), "token-peer")
		reqBody, _ := json.Marshal(map[string]string{
			"nodeId":      peerID.NodeID,
			"publicKey":   hexEncode(peerID.PublicKey),
			"fingerprint": peerID.Fingerprint,
		})
		resp, err := http.Post(httpSrv.URL+"/peer/register", "application/json",
			bytes.NewReader(reqBody))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Errorf("expected 403, got %d", resp.StatusCode)
		}
	})

	// Test: valid token
	t.Run("valid token accepted", func(t *testing.T) {
		peerID, _ := mesh.LoadOrCreateIdentity(t.TempDir(), "token-peer-2")
		reqBody, _ := json.Marshal(map[string]string{
			"nodeId":      peerID.NodeID,
			"publicKey":   hexEncode(peerID.PublicKey),
			"fingerprint": peerID.Fingerprint,
			"token":       "secret123",
		})
		resp, err := http.Post(httpSrv.URL+"/peer/register", "application/json",
			bytes.NewReader(reqBody))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		var body map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&body)
		if body["status"] != "registered" {
			t.Errorf("status = %v", body["status"])
		}
	})
}

// TestPeerRegister_DuplicateRegistration updates existing peer.
func TestPeerRegister_DuplicateRegistration(t *testing.T) {
	identity, err := mesh.LoadOrCreateIdentity(t.TempDir(), "dup-register-node")
	if err != nil {
		t.Fatalf("create identity: %v", err)
	}
	ts := mesh.NewTrustStore(filepath.Join(t.TempDir(), "trusted_peers.json"))

	sv := &Server{
		identity:       identity,
		trustStore:     ts,
		registerPolicy: "open",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/peer/register", sv.handlePeerRegister)
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	peerID, _ := mesh.LoadOrCreateIdentity(t.TempDir(), "dup-peer")
	reqBody, _ := json.Marshal(map[string]string{
		"nodeId":      peerID.NodeID,
		"publicKey":   hexEncode(peerID.PublicKey),
		"fingerprint": peerID.Fingerprint,
	})

	// First registration should succeed
	resp1, _ := http.Post(httpSrv.URL+"/peer/register", "application/json", bytes.NewReader(reqBody))
	if resp1.StatusCode != 200 {
		t.Fatal("first registration should succeed")
	}
	resp1.Body.Close()

	// Second registration should also succeed (update)
	resp2, _ := http.Post(httpSrv.URL+"/peer/register", "application/json", bytes.NewReader(reqBody))
	if resp2.StatusCode != 200 {
		t.Errorf("second registration should succeed (update), got %d", resp2.StatusCode)
	}
	resp2.Body.Close()
}

// hexEncode is a helper used by tests.
func hexEncode(b []byte) string {
	const hextable = "0123456789abcdef"
	res := make([]byte, len(b)*2)
	for i, v := range b {
		res[i*2] = hextable[v>>4]
		res[i*2+1] = hextable[v&0x0f]
	}
	return string(res)
}
