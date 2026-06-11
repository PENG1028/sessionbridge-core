package crypto

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
)

// KeyExchange implements a 3-message authenticated key exchange using
// ephemeral X25519 keys signed by long-term ed25519 identities.
//
// This replaces the 6-step ed25519 challenge-response handshake with a
// simpler 3-step protocol that provides:
//   - Mutual authentication (ed25519 signatures)
//   - Perfect forward secrecy (ephemeral X25519 keys)
//   - Bidirectional AES-256-GCM session keys
//
// Flow:
//  1. Init generates ephemeral X25519, sends pub key + signature(nonce || eph_pub)
//  2. Resp generates ephemeral X25519, verifies init's sig, computes ECDH,
//     sends pub key + signature(nonce || eph_pub)
//  3. Init verifies resp's sig, both derive session keys from ECDH

// KeyExchangeState holds the state for one side of a key exchange.
type KeyExchangeState struct {
	role          string // "init" or "resp"
	ephPriv       *ecdh.PrivateKey
	remoteEphPub  *ecdh.PublicKey
	sessionCipher *SessionCipher
	done          bool
}

// NewKeyExchangeInit creates the initiator's key exchange state.
func NewKeyExchangeInit() *KeyExchangeState {
	return &KeyExchangeState{role: "init"}
}

// NewKeyExchangeResp creates the responder's key exchange state.
func NewKeyExchangeResp() *KeyExchangeState {
	return &KeyExchangeState{role: "resp"}
}

// Step1Init generates the initiator's ephemeral key and returns the message to send.
// The message includes: ephemeral public key + ed25519 signature.
func (k *KeyExchangeState) Step1Init(identityPriv ed25519.PrivateKey, nonce []byte) (ephPub []byte, sig []byte, err error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("keyex: generate eph: %w", err)
	}
	k.ephPriv = key
	ephPub = key.PublicKey().Bytes()

	// Sign(nonce || eph_pub)
	msg := append(nonce, ephPub...)
	sig = ed25519.Sign(identityPriv, msg)
	return ephPub, sig, nil
}

// Step1Resp processes the initiator's message and generates the responder's response.
// Returns the initiator's verified public key fingerprint on success.
func (k *KeyExchangeState) Step1Resp(identityPriv ed25519.PrivateKey, identityPub ed25519.PublicKey,
	nonce []byte, initEphPub, initSig []byte) (respEphPub []byte, respSig []byte, err error) {

	// Verify initiator's signature
	msg := append(nonce, initEphPub...)
	if !ed25519.Verify(identityPub, msg, initSig) {
		return nil, nil, fmt.Errorf("keyex: init signature verification failed")
	}

	// Import initiator's ephemeral public key
	k.remoteEphPub, err = ecdh.X25519().NewPublicKey(initEphPub)
	if err != nil {
		return nil, nil, fmt.Errorf("keyex: invalid init eph pub: %w", err)
	}

	// Generate responder's ephemeral key
	respKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("keyex: generate resp eph: %w", err)
	}
	k.ephPriv = respKey
	respEphPub = respKey.PublicKey().Bytes()

	// Sign(nonce || resp_eph_pub || init_eph_pub)
	sigMsg := append(nonce, respEphPub...)
	sigMsg = append(sigMsg, initEphPub...)
	respSig = ed25519.Sign(identityPriv, sigMsg)

	return respEphPub, respSig, nil
}

// Step2Init processes the responder's response and completes the key exchange.
func (k *KeyExchangeState) Step2Init(identityPub ed25519.PublicKey,
	nonce []byte, respEphPub, respSig, initEphPub []byte) error {

	// Verify responder's signature
	sigMsg := append(nonce, respEphPub...)
	sigMsg = append(sigMsg, initEphPub...)
	if !ed25519.Verify(identityPub, sigMsg, respSig) {
		return fmt.Errorf("keyex: resp signature verification failed")
	}

	// Import responder's ephemeral public key
	remotePub, err := ecdh.X25519().NewPublicKey(respEphPub)
	if err != nil {
		return fmt.Errorf("keyex: invalid resp eph pub: %w", err)
	}

	// Create session cipher
	ciph, err := NewSessionCipher(k.ephPriv, remotePub)
	if err != nil {
		return fmt.Errorf("keyex: create cipher: %w", err)
	}
	k.sessionCipher = ciph
	k.done = true
	return nil
}

// Step2Resp completes the key exchange on the responder side.
func (k *KeyExchangeState) Step2Resp() error {
	if k.remoteEphPub == nil || k.ephPriv == nil {
		return fmt.Errorf("keyex: step1 not completed")
	}
	ciph, err := NewSessionCipher(k.ephPriv, k.remoteEphPub)
	if err != nil {
		return fmt.Errorf("keyex: create cipher: %w", err)
	}
	k.sessionCipher = ciph
	k.done = true
	return nil
}

// SessionCipher returns the established session cipher after a successful exchange.
func (k *KeyExchangeState) SessionCipher() *SessionCipher {
	return k.sessionCipher
}

// Done returns true if the key exchange completed.
func (k *KeyExchangeState) Done() bool { return k.done }

