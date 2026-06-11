// Package crypto provides message-level AES-256-GCM encryption for
// SessionBridge Core WebSocket communication.
//
// Phase 1 of the ENCRYPTION.md roadmap:
//   - Ephemeral X25519 key pair per connection (forward secrecy)
//   - ECDH key exchange after ed25519 peer authentication
//   - HKDF-SHA256 key derivation for send/recv keys
//   - AES-256-GCM authenticated encryption
//
// Backward compatibility: encryption is negotiated during handshake.
// When both peers support "encrypt-v1", encryption is enabled.
// Otherwise, messages are sent in plaintext.
package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"sync/atomic"
)

// SessionCipher manages AES-256-GCM encryption for a peer connection.
type SessionCipher struct {
	sendKey   []byte    // AES-256-GCM send key (32 bytes)
	recvKey   []byte    // AES-256-GCM receive key (32 bytes)
	sendNonce uint64    // monotonic nonce counter for sending
	recvNonce uint64    // monotonic nonce counter for receiving
}

// NewSessionCipher creates a SessionCipher from the local private key
// and the remote peer's public key using X25519 ECDH + HKDF.
//
// Key ordering is deterministic: the peer with the lexicographically higher
// public key assigns k1 to sendKey and k2 to recvKey. The peer with the
// lower public key does the opposite. This ensures both sides agree on
// which key encrypts which direction without an explicit initiator flag.
func NewSessionCipher(localPriv *ecdh.PrivateKey, remotePub *ecdh.PublicKey) (*SessionCipher, error) {
	shared, err := localPriv.ECDH(remotePub)
	if err != nil {
		return nil, fmt.Errorf("ecdh: %w", err)
	}

	// Derive two independent keys using different info strings
	k1 := make([]byte, 32)
	k2 := make([]byte, 32)
	hkdfExpand(sha256.New, shared, []byte("sessionbridge-key-1"), k1)
	hkdfExpand(sha256.New, shared, []byte("sessionbridge-key-2"), k2)

	// Deterministic assignment: higher pub key → k1=send, k2=recv
	localPub := localPriv.PublicKey().Bytes()
	if bytes.Compare(localPub, remotePub.Bytes()) > 0 {
		return &SessionCipher{sendKey: k1, recvKey: k2}, nil
	}
	return &SessionCipher{sendKey: k2, recvKey: k1}, nil
}

// GenerateEphemeralKey creates a new X25519 key pair for one session.
func GenerateEphemeralKey() (*ecdh.PrivateKey, error) {
	return ecdh.X25519().GenerateKey(rand.Reader)
}

// Encrypt encrypts plaintext using AES-256-GCM with the send key.
// Returns (ciphertext, 12-byte nonce).
func (c *SessionCipher) Encrypt(plaintext []byte) ([]byte, []byte) {
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint64(nonce[4:], atomic.AddUint64(&c.sendNonce, 1))

	block, _ := aes.NewCipher(c.sendKey)
	gcm, _ := cipher.NewGCM(block)
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce
}

// Decrypt decrypts ciphertext using AES-256-GCM with the receive key.
func (c *SessionCipher) Decrypt(ciphertext, nonce []byte) ([]byte, error) {
	block, err := aes.NewCipher(c.recvKey)
	if err != nil {
		return nil, fmt.Errorf("aes new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plaintext, nil
}

// EncryptedOverhead is the extra bytes added by encryption (nonce + GCM tag).
const EncryptedOverhead = 12 + 16

// hkdfExpand implements HKDF-Expand (RFC 5869 section 2.3) without external deps.
func hkdfExpand(h func() hash.Hash, secret, info []byte, out []byte) {
	// HKDF-Extract: PRK = HMAC(salt=hash_len_zeros, secret)
	// Use nil salt (all zeros of hash length)
	salt := make([]byte, h().Size())
	mac := hmac.New(h, salt)
	mac.Write(secret)
	prk := mac.Sum(nil)

	// HKDF-Expand: T(1) = HMAC(PRK, info || 0x01)
	mac = hmac.New(h, prk)
	mac.Write(info)
	mac.Write([]byte{1})
	t := mac.Sum(nil)
	copy(out, t[:len(out)])
}
