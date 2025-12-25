package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
)

// EncryptWithDataKey encrypts data using AES-256-GCM
// Format: [version (1 byte)][nonce (12 bytes)][encrypted data][auth tag (16 bytes)]
// This is the new encryption scheme used when dataEncryptionKey is set
func EncryptWithDataKey(data interface{}, dataKey []byte) ([]byte, error) {
	if len(dataKey) != 32 {
		return nil, fmt.Errorf("data key must be 32 bytes")
	}

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

	// Create AES cipher
	block, err := aes.NewCipher(dataKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate random nonce (12 bytes for GCM)
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt (includes auth tag)
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	// Bundle: version(1) + nonce(12) + ciphertext + authTag(16)
	result := make([]byte, 1+12+len(ciphertext))
	result[0] = 0 // version
	copy(result[1:13], nonce)
	copy(result[13:], ciphertext)

	return result, nil
}

// DecryptWithDataKey decrypts data using AES-256-GCM
func DecryptWithDataKey(encrypted []byte, dataKey []byte, target interface{}) error {
	if len(dataKey) != 32 {
		return fmt.Errorf("data key must be 32 bytes")
	}

	if len(encrypted) < 1+12+16 {
		return fmt.Errorf("encrypted data too short")
	}

	// Check version
	if encrypted[0] != 0 {
		return fmt.Errorf("unsupported encryption version: %d", encrypted[0])
	}

	// Extract nonce
	nonce := encrypted[1:13]

	// Extract ciphertext + auth tag
	ciphertext := encrypted[13:]

	// Create AES cipher
	block, err := aes.NewCipher(dataKey)
	if err != nil {
		return fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("failed to create GCM: %w", err)
	}

	// Decrypt
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return fmt.Errorf("decryption failed: %w", err)
	}

	// Deserialize JSON
	if err := json.Unmarshal(plaintext, target); err != nil {
		return fmt.Errorf("failed to unmarshal data: %w", err)
	}

	return nil
}
