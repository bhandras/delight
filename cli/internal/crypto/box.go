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

// GenerateBoxKeyPair generates a new X25519 key pair for crypto_box encryption
// Returns (publicKey [32]byte, privateKey [32]byte, error)
func GenerateBoxKeyPair() (*[32]byte, *[32]byte, error) {
	publicKey, privateKey, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate box keypair: %w", err)
	}
	return publicKey, privateKey, nil
}

// DecryptAuthResponse decrypts the auth response from mobile app
// Handles both V1 (legacy 32-byte secret) and V2 (33-byte: version + dataKey) formats
//
// V1 format: Just 32 bytes of decrypted data = account secret
// V2 format: [version byte = 0x00][32-byte dataEncryptionKey]
//
// Returns:
//   - secret: The decrypted secret/key (32 bytes)
//   - isV2: True if this was a V2 format response
//   - error: Any decryption error
func DecryptAuthResponse(encrypted []byte, recipientSecretKey *[32]byte) ([]byte, bool, error) {
	decrypted, err := DecryptBox(encrypted, recipientSecretKey)
	if err != nil {
		return nil, false, err
	}

	// V2 format: [version=0x00][32-byte dataEncryptionKey]
	if len(decrypted) == 33 && decrypted[0] == 0x00 {
		// Extract the 32-byte dataEncryptionKey after the version byte
		return decrypted[1:], true, nil
	}

	// V1 format: Plain 32-byte legacy secret
	if len(decrypted) == 32 {
		return decrypted, false, nil
	}

	return nil, false, fmt.Errorf("unexpected decrypted data length: %d (expected 32 or 33)", len(decrypted))
}
