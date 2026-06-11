package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

// TestKeyExchange_FullRoundTrip tests a complete 3-step key exchange.
func TestKeyExchange_FullRoundTrip(t *testing.T) {
	// Generate ed25519 identities for both parties
	initPub, initPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("init key: %v", err)
	}
	respPub, respPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("resp key: %v", err)
	}

	// Shared nonce (from challenge-response)
	nonce := make([]byte, 32)
	rand.Read(nonce)

	// Step 1: Initiator generates ephemeral key + signature
	initKE := NewKeyExchangeInit()
	initEphPub, initSig, err := initKE.Step1Init(initPriv, nonce)
	if err != nil {
		t.Fatalf("step1 init: %v", err)
	}
	if len(initEphPub) != 32 {
		t.Fatalf("init eph pub len = %d, want 32", len(initEphPub))
	}
	if len(initSig) != ed25519.SignatureSize {
		t.Fatalf("init sig len = %d, want %d", len(initSig), ed25519.SignatureSize)
	}

	// Step 1.5: Responder verifies init's signature and generates response
	respKE := NewKeyExchangeResp()
	respEphPub, respSig, err := respKE.Step1Resp(respPriv, initPub, nonce, initEphPub, initSig)
	if err != nil {
		t.Fatalf("step1 resp: %v", err)
	}
	if len(respEphPub) != 32 {
		t.Fatalf("resp eph pub len = %d, want 32", len(respEphPub))
	}

	// Step 2: Initiator verifies resp's signature
	err = initKE.Step2Init(respPub, nonce, respEphPub, respSig, initEphPub)
	if err != nil {
		t.Fatalf("step2 init: %v", err)
	}

	// Step 2.5: Responder completes
	err = respKE.Step2Resp()
	if err != nil {
		t.Fatalf("step2 resp: %v", err)
	}

	// Both sides should have session ciphers
	if !initKE.Done() {
		t.Fatal("init ke not done")
	}
	if !respKE.Done() {
		t.Fatal("resp ke not done")
	}

	initCiph := initKE.SessionCipher()
	respCiph := respKE.SessionCipher()
	if initCiph == nil || respCiph == nil {
		t.Fatal("session cipher is nil")
	}

	// Test bidirectional encryption
	msg := []byte("Hello KeyExchange!")
	ct, nonceVal := initCiph.Encrypt(msg)
	decrypted, err := respCiph.Decrypt(ct, nonceVal)
	if err != nil {
		t.Fatalf("init->resp decrypt: %v", err)
	}
	if string(decrypted) != string(msg) {
		t.Errorf("init->resp: got %q, want %q", string(decrypted), string(msg))
	}

	// Responder sends
	ct2, nonceVal2 := respCiph.Encrypt(msg)
	decrypted2, err := initCiph.Decrypt(ct2, nonceVal2)
	if err != nil {
		t.Fatalf("resp->init decrypt: %v", err)
	}
	if string(decrypted2) != string(msg) {
		t.Errorf("resp->init: got %q, want %q", string(decrypted2), string(msg))
	}

	t.Log("3-step key exchange + bidirectional encryption OK")
}

// TestKeyExchange_WrongSignature verifies wrong signatures are rejected.
func TestKeyExchange_WrongSignature(t *testing.T) {
	initPub, initPriv, _ := ed25519.GenerateKey(rand.Reader)
	_, respPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate resp key: %v", err)
	}
	respPub := respPriv.Public().(ed25519.PublicKey)
	_, wrongRespPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate wrong key: %v", err)
	}

	nonce := make([]byte, 32)
	initKE := NewKeyExchangeInit()
	initEphPub, initSig, _ := initKE.Step1Init(initPriv, nonce)

	// Responder uses wrong identity key
	respKE := NewKeyExchangeResp()
	respEphPub, respSig, err := respKE.Step1Resp(wrongRespPriv, initPub, nonce, initEphPub, initSig)
	if err != nil {
		t.Fatalf("step1 resp should succeed (init sig is valid): %v", err)
	}

	// Initiator verifies with correct respPub — this should fail
	err = initKE.Step2Init(respPub, nonce, respEphPub, respSig, initEphPub)
	if err == nil {
		t.Fatal("should reject responder signed with wrong key")
	}
	t.Logf("wrong responder identity correctly rejected: %v", err)

	// Also test wrong initiator signature
	_, wrongInitPriv, _ := ed25519.GenerateKey(rand.Reader)
	initKE2 := NewKeyExchangeInit()
	_, wrongSig, _ := initKE2.Step1Init(wrongInitPriv, nonce) // signed with wrong key

	respKE2 := NewKeyExchangeResp()
	_, _, err = respKE2.Step1Resp(respPriv, initPub, nonce, initEphPub, wrongSig)
	if err == nil {
		t.Fatal("should reject initiator signed with wrong key")
	}
	t.Logf("wrong initiator identity correctly rejected: %v", err)
}

// TestKeyExchange_WrongNonce verifies different nonces fail.
func TestKeyExchange_WrongNonce(t *testing.T) {
	initPub, initPriv, _ := ed25519.GenerateKey(rand.Reader)
	respPub, respPriv, _ := ed25519.GenerateKey(rand.Reader)

	nonce1 := make([]byte, 32)
	nonce2 := make([]byte, 32)
	nonce2[0] = 0x01

	initKE := NewKeyExchangeInit()
	initEphPub, initSig, _ := initKE.Step1Init(initPriv, nonce1)

	// Responder uses different nonce
	respKE := NewKeyExchangeResp()
	_, _, err := respKE.Step1Resp(respPriv, initPub, nonce2, initEphPub, initSig)
	if err == nil {
		t.Fatal("should reject wrong nonce")
	}
	t.Logf("wrong nonce correctly rejected: %v", err)

	// But same nonce should work
	respKE2 := NewKeyExchangeResp()
	respEphPub, respSig, err := respKE2.Step1Resp(respPriv, initPub, nonce1, initEphPub, initSig)
	if err != nil {
		t.Fatalf("correct nonce rejected: %v", err)
	}

	err = initKE.Step2Init(respPub, nonce1, respEphPub, respSig, initEphPub)
	if err != nil {
		t.Fatalf("step2 init with correct nonce: %v", err)
	}
}

// TestKeyExchange_DifferentKeys verifies different key pairs produce different ciphers.
func TestKeyExchange_DifferentKeys(t *testing.T) {
	initPub1, initPriv1, _ := ed25519.GenerateKey(rand.Reader)
	respPub1, respPriv1, _ := ed25519.GenerateKey(rand.Reader)
	nonce := make([]byte, 32)

	// Exchange 1
	initKE1 := NewKeyExchangeInit()
	iep1, isig1, _ := initKE1.Step1Init(initPriv1, nonce)
	respKE1 := NewKeyExchangeResp()
	rep1, rsig1, _ := respKE1.Step1Resp(respPriv1, initPub1, nonce, iep1, isig1)
	initKE1.Step2Init(respPub1, nonce, rep1, rsig1, iep1)
	respKE1.Step2Resp()

	// Exchange 2 (different keys)
	initPub2, initPriv2, _ := ed25519.GenerateKey(rand.Reader)
	respPub2, respPriv2, _ := ed25519.GenerateKey(rand.Reader)
	initKE2 := NewKeyExchangeInit()
	iep2, isig2, _ := initKE2.Step1Init(initPriv2, nonce)
	respKE2 := NewKeyExchangeResp()
	rep2, rsig2, _ := respKE2.Step1Resp(respPriv2, initPub2, nonce, iep2, isig2)
	initKE2.Step2Init(respPub2, nonce, rep2, rsig2, iep2)
	respKE2.Step2Resp()

	// Ciphers should be different
	c1 := initKE1.SessionCipher()
	c2 := initKE2.SessionCipher()

	// Try decrypting exchange1's ciphertext with exchange2's cipher
	ct, nv := c1.Encrypt([]byte("test"))
	_, err := c2.Decrypt(ct, nv)
	if err == nil {
		t.Error("should not decrypt with wrong cipher")
	}
}
