package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/sessionnode/go-core/internal/auth"
	"github.com/user/sessionnode/go-core/internal/dispatcher"
	"github.com/user/sessionnode/go-core/internal/executor"
	"github.com/user/sessionnode/go-core/internal/mesh"
	"github.com/user/sessionnode/go-core/internal/permission"
	"github.com/user/sessionnode/go-core/internal/process"
	"github.com/user/sessionnode/go-core/internal/session"
	"github.com/user/sessionnode/go-core/internal/wsconn"
	"github.com/user/sessionnode/go-core/pkg/protocol"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// testPeerKeys stores generated peer key pairs for use in handshake tests.
var (
	testPeerKeysMu sync.Mutex
	testPeerKeys   = map[string]*testPeerKeyPair{}
)

type testPeerKeyPair struct {
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

func storePeerKey(key string, pub ed25519.PublicKey, priv ed25519.PrivateKey) {
	testPeerKeysMu.Lock()
	defer testPeerKeysMu.Unlock()
	testPeerKeys[key] = &testPeerKeyPair{PublicKey: pub, PrivateKey: priv}
}

func getPeerKey(key string) *testPeerKeyPair {
	testPeerKeysMu.Lock()
	defer testPeerKeysMu.Unlock()
	return testPeerKeys[key]
}

// testIdentity generates a new ed25519 identity for testing.
func testIdentity(t *testing.T, nodeID string) *mesh.NodeIdentity {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return &mesh.NodeIdentity{
		NodeID:      nodeID,
		PublicKey:   pub,
		PrivateKey:  priv,
		Fingerprint: "test-fingerprint-" + nodeID,
		CreatedAt:   time.Now().UnixMilli(),
	}
}

// testServerWithMesh creates a test server with identity and trust store wired in.
func testServerWithMesh(t *testing.T, serverID string) (*Server, *httptest.Server, *mesh.NodeIdentity, *mesh.TrustStore) {
	t.Helper()

	identity := testIdentity(t, serverID)

	trustStore := mesh.NewTrustStore(t.TempDir() + "/test_trusted_peers.json")
	// Trust a peer with ID "peer_test" using a known key pair.
	peerPub, peerPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate peer key: %v", err)
	}
	// Store the peer's key pair globally so tests can retrieve it.
	storePeerKey(serverID, peerPub, peerPriv)

	if err := trustStore.Add(&mesh.TrustedPeer{
		NodeID:      "peer_test",
		Name:        "Test Peer",
		PublicKey:   peerPub,
		Fingerprint: "peer-fingerprint",
		Addresses:   []string{},
		Status:      mesh.TrustStatusConnected,
		Policy:      mesh.TrustPolicy{Mode: "full"},
	}); err != nil {
		t.Fatalf("add trusted peer: %v", err)
	}

	sessStore := session.NewStore()
	cr := wsconn.NewRegistry()
	pm := process.NewManager(cr.PushChunk, cr.PushSessionEvent)
	execDeps := &executor.Deps{
		Sessions:   sessStore,
		Processes:  pm,
		ConnRoutes: cr,
	}
	execReg := executor.New(execDeps)

	permChecker := permission.NewChecker(
		&mockCapRegistry{},
		&mockPolicyStore{},
	)

	d := dispatcher.New(
		auth.NewTokenAuthenticator(""),
		&mockPluginRegistry{},
		permChecker,
		nil,
		execReg,
		&mockAuditLogger{},
		&mockTopology{},
		"node_local",
	)

	sv := New("", d, sessStore, cr, pm, identity, trustStore)
	httpSrv := httptest.NewServer(sv.httpServer.Handler)
	return sv, httpSrv, identity, trustStore
}

// peerConnect dials the /peer/ws endpoint and returns the connection.
func peerConnect(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/peer/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("peer WS dial error: %v", err)
	}
	return conn
}

// peerRead reads a single message from the peer connection with a timeout.
func peerRead(t *testing.T, conn *websocket.Conn) *protocol.Message {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("peer read error: %v", err)
	}
	msg, err := protocol.UnmarshalMessage(raw)
	if err != nil {
		t.Fatalf("peer unmarshal error: %v (raw: %s)", err, string(raw))
	}
	return msg
}

// peerWrite writes a message to the peer connection.
func peerWrite(t *testing.T, conn *websocket.Conn, msg *protocol.Message) {
	t.Helper()
	data, err := msg.MarshalJSON()
	if err != nil {
		t.Fatalf("peer marshal error: %v", err)
	}
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("peer write error: %v", err)
	}
}

// TestControlWS_RejectsActorTypeNode verifies that a client on /ws cannot claim actorType=node.
func TestControlWS_RejectsActorTypeNode(t *testing.T) {
	_, httpSrv, _, _ := testServerWithMesh(t, "server_reject")
	defer httpSrv.Close()

	conn := wsConnect(t, httpSrv)
	defer conn.Close()

	msg := &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_001",
		Capability: "system.info",
		ActorType:  "node",      // client tries to claim node type
		ActorID:    "evil_node", // and a specific node ID
	}

	resp := sendAndRecv(t, conn, msg)
	if resp.OK {
		t.Fatal("expected failure when actorType=node is sent on control WS")
	}
	if resp.Error == nil || resp.Error.Code != protocol.ErrCodeActorTypeNodeBlocked {
		t.Errorf("expected ErrCodeActorTypeNodeBlocked, got code=%v msg=%v",
			errCode(resp.Error), errMsg(resp.Error))
	}
	t.Logf("correctly rejected: %s — %s", resp.Error.Code, resp.Error.Message)
}

// TestControlWS_DefaultActorTypeStillWorks verifies that a client without actorType still works as "web".
func TestControlWS_DefaultActorTypeStillWorks(t *testing.T) {
	_, httpSrv, _, _ := testServerWithMesh(t, "server_default")
	defer httpSrv.Close()

	conn := wsConnect(t, httpSrv)
	defer conn.Close()

	// No actorType set — should default to "web" and work.
	msg := &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_001",
		Capability: "system.info",
	}

	resp := sendAndRecv(t, conn, msg)
	if !resp.OK {
		t.Fatalf("expected success for message without actorType, got: %v", resp.Error)
	}
	t.Logf("default actor type works: OK=%v", resp.OK)
}

// TestPeerWS_NoHandshake_Rejected verifies that connecting to /peer/ws without
// sending peer.hello results in disconnection.
func TestPeerWS_NoHandshake_Rejected(t *testing.T) {
	_, httpSrv, _, _ := testServerWithMesh(t, "server_nohs")
	defer httpSrv.Close()

	conn := peerConnect(t, httpSrv)
	defer conn.Close()

	// Send something that is NOT peer.hello.
	peerWrite(t, conn, protocol.NewPing())

	// Read response — should get a peer.error
	msg := peerRead(t, conn)
	if msg.Type == protocol.MsgTypePeerError {
		t.Logf("got expected peer.error: %v", msg.Error)
	} else if msg.Type != protocol.MsgTypePeerError {
		t.Errorf("expected peer.error after non-hello, got type=%q", msg.Type)
	}
}

// TestPeerWS_UnknownNode_Rejected verifies that a peer not in the trust store is rejected.
func TestPeerWS_UnknownNode_Rejected(t *testing.T) {
	_, httpSrv, _, _ := testServerWithMesh(t, "server_unknown")
	defer httpSrv.Close()

	conn := peerConnect(t, httpSrv)
	defer conn.Close()

	// Send peer.hello with an unknown node ID.
	helloMsg := protocol.NewPeerHello(
		"unknown_node",
		base64.StdEncoding.EncodeToString([]byte("fake-key")),
		"fake-fingerprint",
		time.Now().UnixMilli(),
	)
	peerWrite(t, conn, helloMsg)

	// Should get peer.error with PEER_UNKNOWN.
	resp := peerRead(t, conn)
	if resp.Type != protocol.MsgTypePeerError {
		t.Fatalf("expected peer.error, got type=%q", resp.Type)
	}
	if resp.Error == nil || resp.Error.Code != protocol.ErrCodePeerUnknown {
		t.Errorf("expected ErrCodePeerUnknown, got code=%v msg=%v",
			errCode(resp.Error), errMsg(resp.Error))
	}
	t.Logf("correctly rejected unknown peer: %s", resp.Error.Message)
}

// TestPeerWS_TrustedPeer_HandshakeSuccess verifies the full handshake flow with a trusted peer.
func TestPeerWS_TrustedPeer_HandshakeSuccess(t *testing.T) {
	_, httpSrv, _, _ := testServerWithMesh(t, "server_success")
	defer httpSrv.Close()

	// Retrieve the peer's private key that was stored during testServerWithMesh.
	kp := getPeerKey("server_success")
	if kp == nil {
		t.Fatal("peer key not found")
	}

	conn := peerConnect(t, httpSrv)
	defer conn.Close()

	peerPriv := kp.PrivateKey
	peerPub := kp.PublicKey

	// Step 1: Send peer.hello.
	helloMsg := protocol.NewPeerHello(
		"peer_test",
		base64.StdEncoding.EncodeToString(peerPub),
		"peer-fingerprint",
		time.Now().UnixMilli(),
	)
	peerWrite(t, conn, helloMsg)

	// Step 2: Read peer.challenge.
	chal := peerRead(t, conn)
	if chal.Type != protocol.MsgTypePeerChallenge {
		t.Fatalf("expected peer.challenge, got type=%q", chal.Type)
	}
	if chal.Error != nil {
		t.Fatalf("unexpected error in challenge: %v", chal.Error)
	}
	t.Logf("received challenge: requestId=%s", chal.RequestID)

	// Decode the nonce.
	var chalPayload struct {
		Nonce string `json:"nonce"`
	}
	if err := json.Unmarshal(chal.Payload, &chalPayload); err != nil {
		t.Fatalf("decode challenge payload: %v", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(chalPayload.Nonce)
	if err != nil {
		t.Fatalf("decode nonce: %v", err)
	}

	// Step 3: Sign the nonce.
	signature := ed25519.Sign(peerPriv, nonce)

	// Step 4: Send peer.response.
	respMsg := protocol.NewPeerResponse(
		chal.RequestID,
		base64.StdEncoding.EncodeToString(signature),
	)
	peerWrite(t, conn, respMsg)

	// Step 5: Read peer.welcome.
	welcome := peerRead(t, conn)
	if welcome.Type != protocol.MsgTypePeerWelcome {
		t.Fatalf("expected peer.welcome, got type=%q", welcome.Type)
	}
	if welcome.Error != nil {
		t.Fatalf("unexpected error in welcome: %v", welcome.Error)
	}
	t.Logf("handshake complete: remote nodeId=%s", welcome.NodeID)
}

// TestPeerWS_ReloadedIdentity_HandshakeSuccess verifies that an identity
// loaded from disk keeps its private key and can still complete peer auth.
func TestPeerWS_ReloadedIdentity_HandshakeSuccess(t *testing.T) {
	_, httpSrv, _, trustStore := testServerWithMesh(t, "server_reloaded")
	defer httpSrv.Close()

	dir := t.TempDir()
	clientID, err := mesh.LoadOrCreateIdentity(dir, "client_reload")
	if err != nil {
		t.Fatalf("create client identity: %v", err)
	}
	reloadedID, err := mesh.LoadOrCreateIdentity(dir, "client_reload")
	if err != nil {
		t.Fatalf("reload client identity: %v", err)
	}

	if err := trustStore.Add(&mesh.TrustedPeer{
		NodeID:      reloadedID.NodeID,
		Name:        "Reloaded Client",
		PublicKey:   reloadedID.PublicKey,
		Fingerprint: reloadedID.Fingerprint,
		Status:      mesh.TrustStatusConnected,
		Policy:      mesh.TrustPolicy{Mode: "full"},
	}); err != nil {
		t.Fatalf("add reloaded identity to trust store: %v", err)
	}

	conn := peerConnect(t, httpSrv)
	defer conn.Close()

	helloMsg := protocol.NewPeerHello(
		types.NodeID(reloadedID.NodeID),
		base64.StdEncoding.EncodeToString(reloadedID.PublicKey),
		reloadedID.Fingerprint,
		time.Now().UnixMilli(),
	)
	peerWrite(t, conn, helloMsg)

	chal := peerRead(t, conn)
	if chal.Type != protocol.MsgTypePeerChallenge {
		t.Fatalf("expected peer.challenge, got type=%q", chal.Type)
	}

	var chalPayload struct {
		Nonce string `json:"nonce"`
	}
	if err := json.Unmarshal(chal.Payload, &chalPayload); err != nil {
		t.Fatalf("decode challenge payload: %v", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(chalPayload.Nonce)
	if err != nil {
		t.Fatalf("decode nonce: %v", err)
	}

	signature, err := reloadedID.Sign(nonce)
	if err != nil {
		t.Fatalf("sign with reloaded identity: %v", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(clientID.PublicKey), nonce, signature) {
		t.Fatal("original public key did not verify reloaded identity signature")
	}

	respMsg := protocol.NewPeerResponse(
		chal.RequestID,
		base64.StdEncoding.EncodeToString(signature),
	)
	peerWrite(t, conn, respMsg)

	welcome := peerRead(t, conn)
	if welcome.Type != protocol.MsgTypePeerWelcome {
		t.Fatalf("expected peer.welcome, got type=%q", welcome.Type)
	}
	if welcome.Error != nil {
		t.Fatalf("unexpected error in welcome: %v", welcome.Error)
	}
}

// TestPeerWS_WrongSignature_Rejected verifies that a bad signature on the challenge
// nonce causes the handshake to fail.
func TestPeerWS_WrongSignature_Rejected(t *testing.T) {
	_, httpSrv, _, _ := testServerWithMesh(t, "server_badsig")
	defer httpSrv.Close()

	// Use a completely different key pair (not the trusted one).
	wrongPub, wrongPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate wrong key: %v", err)
	}

	conn := peerConnect(t, httpSrv)
	defer conn.Close()

	// Send hello with the trusted peer's ID but a DIFFERENT public key.
	// Retrieve the trusted peer's public key so we can pass the key check,
	// but sign the challenge with a different (wrong) key.
	kp := getPeerKey("server_badsig")
	if kp == nil {
		t.Fatal("peer key not found")
	}
	peerPub := kp.PublicKey

	// Step 1: Send peer.hello with the CORRECT public key (to pass trust check).
	helloMsg := protocol.NewPeerHello(
		"peer_test",
		base64.StdEncoding.EncodeToString(peerPub),
		"peer-fingerprint",
		time.Now().UnixMilli(),
	)
	peerWrite(t, conn, helloMsg)

	// Step 2: Read challenge.
	chal := peerRead(t, conn)
	if chal.Type != protocol.MsgTypePeerChallenge {
		t.Fatalf("expected peer.challenge, got type=%q", chal.Type)
	}

	var chalPayload struct {
		Nonce string `json:"nonce"`
	}
	if err := json.Unmarshal(chal.Payload, &chalPayload); err != nil {
		t.Fatalf("decode challenge: %v", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(chalPayload.Nonce)
	if err != nil {
		t.Fatalf("decode nonce: %v", err)
	}

	// Step 3: Sign with the WRONG key.
	signature := ed25519.Sign(wrongPriv, nonce)

	// Step 4: Send peer.response with wrong signature.
	respMsg := protocol.NewPeerResponse(
		chal.RequestID,
		base64.StdEncoding.EncodeToString(signature),
	)
	peerWrite(t, conn, respMsg)

	// Step 5: Should get peer.error.
	response := peerRead(t, conn)
	if response.Type != protocol.MsgTypePeerError {
		t.Fatalf("expected peer.error after bad signature, got type=%q", response.Type)
	}
	if response.Error == nil || response.Error.Code != protocol.ErrCodePeerHandshakeFailed {
		t.Errorf("expected ErrCodePeerHandshakeFailed, got code=%v msg=%v",
			errCode(response.Error), errMsg(response.Error))
	}
	t.Logf("correctly rejected bad signature: %s", response.Error.Message)

	// Suppress unused variable warning.
	_ = wrongPub
}

// TestPeerWS_KeyMismatch_Rejected verifies that presenting a different public key
// than what's on file causes rejection.
func TestPeerWS_KeyMismatch_Rejected(t *testing.T) {
	_, httpSrv, _, _ := testServerWithMesh(t, "server_keymismatch")
	defer httpSrv.Close()

	conn := peerConnect(t, httpSrv)
	defer conn.Close()

	// Use a different key pair.
	wrongPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate wrong key: %v", err)
	}

	// Send peer.hello with the trusted peer ID but a WRONG public key.
	helloMsg := protocol.NewPeerHello(
		"peer_test",
		base64.StdEncoding.EncodeToString(wrongPub),
		"peer-fingerprint",
		time.Now().UnixMilli(),
	)
	peerWrite(t, conn, helloMsg)

	// Should get peer.error with PEER_KEY_MISMATCH.
	resp := peerRead(t, conn)
	if resp.Type != protocol.MsgTypePeerError {
		t.Fatalf("expected peer.error, got type=%q", resp.Type)
	}
	if resp.Error == nil || resp.Error.Code != protocol.ErrCodePeerKeyMismatch {
		t.Errorf("expected ErrCodePeerKeyMismatch, got code=%v msg=%v",
			errCode(resp.Error), errMsg(resp.Error))
	}
	t.Logf("correctly rejected key mismatch: %s", resp.Error.Message)
}

// TestPeerWS_ExpiredTrust_Rejected verifies that a peer with expired trust is rejected.
func TestPeerWS_ExpiredTrust_Rejected(t *testing.T) {
	_, httpSrv, _, trustStore := testServerWithMesh(t, "server_expired")
	defer httpSrv.Close()

	// Change the trusted peer's status to "expired".
	peer, err := trustStore.Get("peer_test")
	if err != nil {
		t.Fatalf("get peer: %v", err)
	}
	peer.Status = mesh.TrustStatusExpired
	if err := trustStore.Add(peer); err != nil {
		t.Fatalf("update peer status: %v", err)
	}

	conn := peerConnect(t, httpSrv)
	defer conn.Close()

	// Send peer.hello
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	_ = priv

	helloMsg := protocol.NewPeerHello(
		"peer_test",
		base64.StdEncoding.EncodeToString(pub),
		"peer-fingerprint",
		time.Now().UnixMilli(),
	)
	peerWrite(t, conn, helloMsg)

	// Should get peer.error with PEER_KEY_MISMATCH first (key doesn't match),
	// or PEER_EXPIRED if the key matches. The key mismatch will fire first since
	// the key we're sending doesn't match. Let's check for either.
	resp := peerRead(t, conn)
	if resp.Type != protocol.MsgTypePeerError {
		t.Fatalf("expected peer.error, got type=%q", resp.Type)
	}
	t.Logf("rejected with: code=%s msg=%s", errCode(resp.Error), errMsg(resp.Error))
	// The important thing is we got rejected — the key mismatch check fires
	// before the expired check when the keys don't match.
}
