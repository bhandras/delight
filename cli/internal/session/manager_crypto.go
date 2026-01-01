package session

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"

	"github.com/bhandras/delight/cli/internal/claude"
	"github.com/bhandras/delight/cli/internal/crypto"
)

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// encrypt encrypts data using the session key
func (m *Manager) encrypt(data []byte) (string, error) {
	if m.dataKey == nil {
		return "", fmt.Errorf("session dataEncryptionKey is not loaded")
	}
	encrypted, err := crypto.EncryptWithDataKey(json.RawMessage(data), m.dataKey)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

// decrypt decrypts base64-encoded data using the session key
// Detects encryption format: AES-GCM (version byte 0) vs legacy SecretBox
func (m *Manager) decrypt(dataB64 string) ([]byte, error) {
	encrypted, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64: %w", err)
	}

	if len(encrypted) == 0 {
		return nil, fmt.Errorf("empty encrypted data")
	}

	if m.debug {
		log.Printf("[decrypt] data length: %d, first bytes: %v", len(encrypted), encrypted[:min(10, len(encrypted))])
		log.Printf("[decrypt] dataKey set: %v, masterSecret set: %v", m.dataKey != nil, m.masterSecret != nil)
	}

	// Check if this is AES-GCM format (version byte 0)
	// AES-GCM format: [version(1)] [nonce(12)] [ciphertext+tag(16+)]
	// Minimum length: 1 + 12 + 16 = 29 bytes
	if encrypted[0] == 0 && len(encrypted) >= 29 {
		if m.debug {
			log.Printf("[decrypt] Detected AES-GCM format (version byte 0)")
		}
		// AES-GCM format is only valid when the session dataEncryptionKey is
		// present. Falling back to the master secret cannot work reliably and
		// masks the real problem (missing key hydration).
		if m.dataKey == nil {
			return nil, fmt.Errorf("AES-GCM encrypted data but dataEncryptionKey is not loaded")
		}
		var result json.RawMessage
		if err := crypto.DecryptWithDataKey(encrypted, m.dataKey, &result); err != nil {
			return nil, fmt.Errorf("failed to decrypt AES-GCM: %w", err)
		}
		return result, nil
	}

	return nil, fmt.Errorf("unsupported encrypted payload format")
}

// encryptMachine encrypts daemon-scoped state using the master secret.
func (m *Manager) encryptMachine(data []byte) (string, error) {
	var secretKey [32]byte
	copy(secretKey[:], m.masterSecret)
	encrypted, err := crypto.EncryptLegacy(json.RawMessage(data), &secretKey)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

// decryptMachine decrypts daemon-scoped state using the master secret.
func (m *Manager) decryptMachine(dataB64 string) ([]byte, error) {
	encrypted, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64: %w", err)
	}

	var secretKey [32]byte
	copy(secretKey[:], m.masterSecret)

	var raw json.RawMessage
	if err := crypto.DecryptLegacy(encrypted, &secretKey, &raw); err != nil {
		return nil, err
	}
	return []byte(raw), nil
}

// encryptRemoteMessage encrypts a remote message for transmission
func (m *Manager) encryptRemoteMessage(msg *claude.RemoteMessage) (string, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return "", err
	}
	return m.encrypt(data)
}
