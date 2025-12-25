package crypto

import (
	"crypto/rand"
	"fmt"

	"golang.org/x/crypto/nacl/box"
)

// EncryptBox encrypts data for a recipient's public key using TweetNaCl Box
// Format: [ephemeral public key (32 bytes)][nonce (24 bytes)][encrypted data]
// Used for QR code authentication response encryption
func EncryptBox(data []byte, recipientPublicKey *[32]byte) ([]byte, error) {
	// Generate ephemeral keypair
	ephemeralPublic, ephemeralPrivate, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate ephemeral keypair: %w", err)
	}

	// Generate random nonce
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt
	encrypted := box.Seal(nil, data, &nonce, recipientPublicKey, ephemeralPrivate)

	// Bundle: ephemeral pubkey + nonce + encrypted
	result := make([]byte, 32+24+len(encrypted))
	copy(result[0:32], ephemeralPublic[:])
	copy(result[32:56], nonce[:])
	copy(result[56:], encrypted)

	return result, nil
}

// DecryptBox decrypts data encrypted with EncryptBox
func DecryptBox(encrypted []byte, recipientSecretKey *[32]byte) ([]byte, error) {
	if len(encrypted) < 32+24 {
		return nil, fmt.Errorf("encrypted data too short")
	}

	// Extract ephemeral public key
	var ephemeralPublic [32]byte
	copy(ephemeralPublic[:], encrypted[0:32])

	// Extract nonce
	var nonce [24]byte
	copy(nonce[:], encrypted[32:56])

	// Extract ciphertext
	ciphertext := encrypted[56:]

	// Decrypt
	decrypted, ok := box.Open(nil, ciphertext, &nonce, &ephemeralPublic, recipientSecretKey)
	if !ok {
		return nil, fmt.Errorf("decryption failed")
	}

	return decrypted, nil
}
