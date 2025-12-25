package crypto

import (
	"crypto/rand"
	"encoding/json"
	"fmt"

	"golang.org/x/crypto/nacl/secretbox"
)

// EncryptLegacy encrypts data using TweetNaCl SecretBox (XSalsa20-Poly1305)
// Format: [nonce (24 bytes)][encrypted data + auth tag]
// Compatible with the original Happy server's legacy encryption
func EncryptLegacy(data interface{}, secret *[32]byte) ([]byte, error) {
	var plaintext []byte
	switch v := data.(type) {
	case json.RawMessage:
		plaintext = []byte(v)
	case []byte:
		plaintext = v
	default:
		// Serialize data to JSON
		encoded, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal data: %w", err)
		}
		plaintext = encoded
	}

	// Generate random nonce
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt
	encrypted := secretbox.Seal(nil, plaintext, &nonce, secret)

	// Combine nonce + encrypted data
	result := make([]byte, 24+len(encrypted))
	copy(result[0:24], nonce[:])
	copy(result[24:], encrypted)

	return result, nil
}

// DecryptLegacy decrypts data using TweetNaCl SecretBox
func DecryptLegacy(encrypted []byte, secret *[32]byte, target interface{}) error {
	if len(encrypted) < 24 {
		return fmt.Errorf("encrypted data too short")
	}

	// Extract nonce
	var nonce [24]byte
	copy(nonce[:], encrypted[0:24])

	// Decrypt
	decrypted, ok := secretbox.Open(nil, encrypted[24:], &nonce, secret)
	if !ok {
		return fmt.Errorf("decryption failed")
	}

	// Deserialize JSON
	if err := json.Unmarshal(decrypted, target); err != nil {
		return fmt.Errorf("failed to unmarshal data: %w", err)
	}

	return nil
}
