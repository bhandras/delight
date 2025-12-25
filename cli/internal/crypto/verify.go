package crypto

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
)

// VerifyAuthChallenge verifies an Ed25519 signature for authentication
// Used in the challenge-response authentication flow
func VerifyAuthChallenge(publicKeyB64, challengeB64, signatureB64 string) (bool, error) {
	// Decode base64 inputs
	publicKey, err := base64.StdEncoding.DecodeString(publicKeyB64)
	if err != nil {
		return false, fmt.Errorf("failed to decode public key: %w", err)
	}

	challenge, err := base64.StdEncoding.DecodeString(challengeB64)
	if err != nil {
		return false, fmt.Errorf("failed to decode challenge: %w", err)
	}

	signature, err := base64.StdEncoding.DecodeString(signatureB64)
	if err != nil {
		return false, fmt.Errorf("failed to decode signature: %w", err)
	}

	// Verify signature
	if len(publicKey) != ed25519.PublicKeySize {
		return false, fmt.Errorf("invalid public key size")
	}

	valid := ed25519.Verify(ed25519.PublicKey(publicKey), challenge, signature)
	return valid, nil
}

// PublicKeyToHex converts a base64 public key to hex for database storage
func PublicKeyToHex(publicKeyB64 string) (string, error) {
	publicKey, err := base64.StdEncoding.DecodeString(publicKeyB64)
	if err != nil {
		return "", fmt.Errorf("failed to decode public key: %w", err)
	}
	return fmt.Sprintf("%x", publicKey), nil
}

// GenerateKeyPair generates a new Ed25519 key pair
// Returns (publicKey, privateKey, error)
func GenerateKeyPair() ([]byte, []byte, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate key pair: %w", err)
	}
	return publicKey, privateKey, nil
}

// Sign signs a message using an Ed25519 private key
func Sign(privateKey, message []byte) ([]byte, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid private key size: %d (expected %d)", len(privateKey), ed25519.PrivateKeySize)
	}
	signature := ed25519.Sign(ed25519.PrivateKey(privateKey), message)
	return signature, nil
}
