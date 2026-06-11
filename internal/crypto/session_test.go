package crypto

import (
	"crypto/ecdh"
	"crypto/rand"
	"testing"
)

// TestSessionCipher_EncryptDecrypt tests a full encrypt/decrypt round-trip.
func TestSessionCipher_EncryptDecrypt(t *testing.T) {
	alicePriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("alice key: %v", err)
	}
	bobPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("bob key: %v", err)
	}

	// Alice creates cipher with her priv + Bob's pub
	aliceCipher, err := NewSessionCipher(alicePriv, bobPriv.PublicKey())
	if err != nil {
		t.Fatalf("alice cipher: %v", err)
	}

	// Bob creates cipher with his priv + Alice's pub
	bobCipher, err := NewSessionCipher(bobPriv, alicePriv.PublicKey())
	if err != nil {
		t.Fatalf("bob cipher: %v", err)
	}

	// Alice encrypts
	plaintext := []byte("Hello, SessionBridge encryption!")
	ciphertext, nonce := aliceCipher.Encrypt(plaintext)
	if len(ciphertext) == 0 {
		t.Fatal("ciphertext is empty")
	}
	if len(nonce) != 12 {
		t.Fatalf("nonce length = %d, want 12", len(nonce))
	}

	// Bob decrypts
	decrypted, err := bobCipher.Decrypt(ciphertext, nonce)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Errorf("decrypted = %q, want %q", string(decrypted), string(plaintext))
	}
}

// TestSessionCipher_TamperedCiphertext verifies GCM authentication catches tampering.
func TestSessionCipher_TamperedCiphertext(t *testing.T) {
	alicePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	bobPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)

	aliceCipher, _ := NewSessionCipher(alicePriv, bobPriv.PublicKey())
	bobCipher, _ := NewSessionCipher(bobPriv, alicePriv.PublicKey())

	plaintext := []byte("secret data")
	ciphertext, nonce := aliceCipher.Encrypt(plaintext)

	// Tamper with ciphertext
	ciphertext[5] ^= 0xFF

	_, err := bobCipher.Decrypt(ciphertext, nonce)
	if err == nil {
		t.Error("expected decryption error for tampered ciphertext")
	}
}

// TestSessionCipher_WrongKey verifies that wrong keys don't decrypt.
func TestSessionCipher_WrongKey(t *testing.T) {
	alicePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	bobPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	evePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)

	aliceCipher, _ := NewSessionCipher(alicePriv, bobPriv.PublicKey())
	eveCipher, _ := NewSessionCipher(evePriv, alicePriv.PublicKey())

	ciphertext, nonce := aliceCipher.Encrypt([]byte("secret"))
	_, err := eveCipher.Decrypt(ciphertext, nonce)
	if err == nil {
		t.Error("expected decryption error for wrong key")
	}
}

// TestSessionCipher_NonceMonotonic verifies nonces are monotonic.
func TestSessionCipher_NonceMonotonic(t *testing.T) {
	alicePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	bobPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)

	aliceCipher, _ := NewSessionCipher(alicePriv, bobPriv.PublicKey())

	_, nonce1 := aliceCipher.Encrypt([]byte("msg1"))
	_, nonce2 := aliceCipher.Encrypt([]byte("msg2"))

	// Nonces should be different and incrementing
	if string(nonce1) == string(nonce2) {
		t.Error("nonces should be unique")
	}

	n1 := uint64(0)
	n2 := uint64(0)
	for i := 0; i < 8; i++ {
		n1 |= uint64(nonce1[4+i]) << (56 - 8*i)
		n2 |= uint64(nonce2[4+i]) << (56 - 8*i)
	}
	if n2 <= n1 {
		t.Error("nonce2 should be > nonce1")
	}
}

// TestSessionCipher_EmptyMessage tests encrypting an empty payload.
func TestSessionCipher_EmptyMessage(t *testing.T) {
	alicePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	bobPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)

	aliceCipher, _ := NewSessionCipher(alicePriv, bobPriv.PublicKey())
	bobCipher, _ := NewSessionCipher(bobPriv, alicePriv.PublicKey())

	ciphertext, nonce := aliceCipher.Encrypt([]byte{})
	decrypted, err := bobCipher.Decrypt(ciphertext, nonce)
	if err != nil {
		t.Fatalf("decrypt empty: %v", err)
	}
	if len(decrypted) != 0 {
		t.Errorf("expected empty, got %d bytes", len(decrypted))
	}
}

// TestGenerateEphemeralKey verifies key generation works.

func TestSessionCipher_DebugECDH(t *testing.T) {
	alicePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	bobPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)

	aliceShared, _ := alicePriv.ECDH(bobPriv.PublicKey())
	bobShared, _ := bobPriv.ECDH(alicePriv.PublicKey())

	t.Logf("alice shared: %x", aliceShared)
	t.Logf("bob shared:   %x", bobShared)
	t.Logf("alice pub:    %x", alicePriv.PublicKey().Bytes())
	t.Logf("bob pub:      %x", bobPriv.PublicKey().Bytes())

	if len(aliceShared) != len(bobShared) {
		t.Fatalf("shared secret length mismatch: %d vs %d", len(aliceShared), len(bobShared))
	}
	for i := range aliceShared {
		if aliceShared[i] != bobShared[i] {
			t.Fatalf("shared secret differs at byte %d: %02x vs %02x", i, aliceShared[i], bobShared[i])
		}
	}
}


func TestSessionCipher_DebugKeys(t *testing.T) {
	alicePriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	bobPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)

	ac, _ := NewSessionCipher(alicePriv, bobPriv.PublicKey())
	bc, _ := NewSessionCipher(bobPriv, alicePriv.PublicKey())

	t.Logf("alice sendKey: %x", ac.sendKey)
	t.Logf("alice recvKey: %x", ac.recvKey)
	t.Logf("bob sendKey:   %x", bc.sendKey)
	t.Logf("bob recvKey:   %x", bc.recvKey)
	
	// Alice's sendKey should equal Bob's recvKey
	// Alice's recvKey should equal Bob's sendKey
}

func TestGenerateEphemeralKey(t *testing.T) {
	key, err := GenerateEphemeralKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	if key == nil {
		t.Fatal("key is nil")
	}
	pub := key.PublicKey()
	if pub == nil {
		t.Fatal("public key is nil")
	}
	pubBytes := pub.Bytes()
	if len(pubBytes) != 32 {
		t.Fatalf("public key length = %d, want 32", len(pubBytes))
	}
}
