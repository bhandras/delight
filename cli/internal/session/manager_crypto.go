package session

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/bhandras/delight/cli/internal/crypto"
	"github.com/bhandras/delight/shared/logger"
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

// decrypt decrypts base64-encoded data using the session key.
func (m *Manager) decrypt(dataB64 string) ([]byte, error) {
	encrypted, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64: %w", err)
	}

	if len(encrypted) == 0 {
		return nil, fmt.Errorf("empty encrypted data")
	}

	if m.debug {
		logger.Tracef("[decrypt] data length: %d, first bytes: %v", len(encrypted), encrypted[:min(10, len(encrypted))])
		logger.Tracef("[decrypt] dataKey set: %v, masterSecret set: %v", m.dataKey != nil, m.masterSecret != nil)
	}

	// Check if this is AES-GCM format (version byte 0)
	// AES-GCM format: [version(1)] [nonce(12)] [ciphertext+tag(16+)]
	// Minimum length: 1 + 12 + 16 = 29 bytes
	if encrypted[0] == 0 && len(encrypted) >= 29 {
		if m.debug {
			logger.Tracef("[decrypt] Detected AES-GCM format (version byte 0)")
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

// encryptTerminal encrypts terminal-scoped state using the master secret.
func (m *Manager) encryptTerminal(data []byte) (string, error) {
	if len(m.masterSecret) != 32 {
		return "", fmt.Errorf("master secret must be 32 bytes, got %d", len(m.masterSecret))
	}
	encrypted, err := crypto.EncryptWithDataKey(json.RawMessage(data), m.masterSecret)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

// decryptTerminal decrypts terminal-scoped state using the master secret.
func (m *Manager) decryptTerminal(dataB64 string) ([]byte, error) {
	encrypted, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64: %w", err)
	}

	var raw json.RawMessage
	if len(m.masterSecret) != 32 {
		return nil, fmt.Errorf("master secret must be 32 bytes, got %d", len(m.masterSecret))
	}
	if err := crypto.DecryptWithDataKey(encrypted, m.masterSecret, &raw); err != nil {
		return nil, err
	}
	return []byte(raw), nil
}
