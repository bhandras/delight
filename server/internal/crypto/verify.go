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
// Handles both standard base64 and URL-safe base64 (with or without padding)
func PublicKeyToHex(publicKeyB64 string) (string, error) {
	// Try standard base64 first
	publicKey, err := base64.StdEncoding.DecodeString(publicKeyB64)
	if err != nil {
		// Try URL-safe base64 with padding
		publicKey, err = base64.URLEncoding.DecodeString(publicKeyB64)
		if err != nil {
			// Try URL-safe base64 without padding (RawURLEncoding)
			publicKey, err = base64.RawURLEncoding.DecodeString(publicKeyB64)
			if err != nil {
				// Try standard base64 without padding (RawStdEncoding)
				publicKey, err = base64.RawStdEncoding.DecodeString(publicKeyB64)
				if err != nil {
					return "", fmt.Errorf("failed to decode public key: %w", err)
				}
			}
		}
	}
	return fmt.Sprintf("%x", publicKey), nil
}

// Base64ToBytes decodes a base64 string to bytes
func Base64ToBytes(b64 string) ([]byte, error) {
	bytes, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64: %w", err)
	}
	return bytes, nil
}

// BytesToBase64 encodes bytes to a base64 string
func BytesToBase64(bytes []byte) string {
	return base64.StdEncoding.EncodeToString(bytes)
}
