package topology

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/PENG1028/sessionbridge-core/internal/crypto"
	"github.com/PENG1028/sessionbridge-core/pkg/protocol"
	"github.com/PENG1028/sessionbridge-core/pkg/types"
)

// TestPeerCipher_SetAndGet verifies cipher setter/getter.
func TestPeerCipher_SetAndGet(t *testing.T) {
	p := newPeer("test-peer", "", nil, StatusDisconnected)

	if c := p.GetCipher(); c != nil {
		t.Error("cipher should be nil initially")
	}

	aliceKey, err := crypto.GenerateEphemeralKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	bobKey, err := crypto.GenerateEphemeralKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	ciph, _ := crypto.NewSessionCipher(aliceKey, bobKey.PublicKey())

	p.SetCipher(ciph)
	if p.GetCipher() == nil {
		t.Fatal("cipher should not be nil after SetCipher")
	}

	// Test encryption works through peer
	plaintext := []byte("forward-test")
	ct, nonce := p.GetCipher().Encrypt(plaintext)

	bobCiph, _ := crypto.NewSessionCipher(bobKey, aliceKey.PublicKey())
	decrypted, err := bobCiph.Decrypt(ct, nonce)
	if err != nil {
		t.Fatalf("decrypt through peer cipher: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Errorf("decrypted = %q, want %q", string(decrypted), string(plaintext))
	}
}

// TestEncryptedPayloadShape verifies that forward() encrypts the payload
// when cipher is set, by reading the raw message from the write channel.
func TestEncryptedPayloadShape(t *testing.T) {
	mainKey, _ := crypto.GenerateEphemeralKey()
	peerKey, _ := crypto.GenerateEphemeralKey()
	mainCiph, _ := crypto.NewSessionCipher(mainKey, peerKey.PublicKey())
	peerCiph, _ := crypto.NewSessionCipher(peerKey, mainKey.PublicKey())

	peer := newPeer("enc-peer", "", nil, StatusConnected)
	peer.writeCh = make(chan []byte, 10)
	peer.SetCipher(mainCiph)

	pt := New(Config{LocalID: "enc-main", LocalName: "enc-main"})
	pt.mu.Lock()
	pt.peers["enc-peer"] = peer
	pt.mu.Unlock()

	// Send forward request in goroutine with a payload
	reqPayload, _ := json.Marshal(map[string]string{"key": "value"})
	go pt.forward(peer, &types.CapabilityRequest{
		RequestID:  "enc_test",
		PluginID:   "test",
		Capability: "system.info",
		Payload:    reqPayload,
	})

	// Read the encrypted message
	select {
	case data := <-peer.writeCh:
		msg, err := protocol.UnmarshalMessage(data)
		if err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(msg.EncryptedPayload) == 0 {
			t.Fatal("EncryptedPayload should be set when cipher is active")
		}
		if msg.Payload != nil {
			t.Error("Payload should be nil when EncryptedPayload is set")
		}
		if len(msg.EncryptNonce) != 12 {
			t.Errorf("EncryptNonce length = %d, want 12", len(msg.EncryptNonce))
		}

		// Decrypt with peer's cipher to verify correctness
		plaintext, err := peerCiph.Decrypt(msg.EncryptedPayload, msg.EncryptNonce)
		if err != nil {
			t.Fatalf("decrypt forwarded message: %v", err)
		}
		var decoded map[string]string
		if err := json.Unmarshal(plaintext, &decoded); err != nil {
			t.Fatalf("unmarshal decrypted payload: %v", err)
		}
		if decoded["key"] != "value" {
			t.Errorf("decrypted payload content = %v, want {key:value}", decoded)
		}
		t.Logf("encrypted forward verified: content=%v size=%d bytes", decoded, len(plaintext))

		// Send a response to unblock the forward goroutine
		respMsg, _ := protocol.UnmarshalMessage(data)
		pt.pendingMu.Lock()
		if ch, ok := pt.pending[respMsg.RequestID]; ok {
			ch <- &types.CapabilityResponse{OK: true}
		}
		pt.pendingMu.Unlock()

	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for encrypted message")
	}
}

// TestPlaintextBackwardCompat verifies forward works without cipher.
func TestPlaintextBackwardCompat(t *testing.T) {
	peer := newPeer("plain-peer", "", nil, StatusConnected)
	peer.writeCh = make(chan []byte, 10)

	pt := New(Config{LocalID: "plain-main", LocalName: "plain-main"})
	pt.mu.Lock()
	pt.peers["plain-peer"] = peer
	pt.mu.Unlock()

	reqPayload, _ := json.Marshal(map[string]string{"key": "value"})
	go pt.forward(peer, &types.CapabilityRequest{
		RequestID:  "plain_test",
		PluginID:   "test",
		Capability: "system.info",
		Payload:    reqPayload,
	})

	select {
	case data := <-peer.writeCh:
		msg, err := protocol.UnmarshalMessage(data)
		if err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(msg.EncryptedPayload) > 0 {
			t.Error("plaintext message should not have EncryptedPayload")
		}
		if msg.Payload == nil {
			t.Fatal("plaintext message should have Payload set")
		}

		// Unblock forward
		pt.pendingMu.Lock()
		if ch, ok := pt.pending[msg.RequestID]; ok {
			ch <- &types.CapabilityResponse{OK: true}
		}
		pt.pendingMu.Unlock()
		t.Log("plaintext backward compat verified")

	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for plaintext message")
	}
}
