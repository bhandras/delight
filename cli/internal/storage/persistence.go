package storage

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GenerateSecretKey generates a new 32-byte secret key
func GenerateSecretKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}
	return key, nil
}

// SaveSecretKey saves the secret key to a file
func SaveSecretKey(path string, key []byte) error {
	// Encode as base64 for readability
	encoded := base64.StdEncoding.EncodeToString(key)

	// Write with restrictive permissions
	if err := os.WriteFile(path, []byte(encoded), 0600); err != nil {
		return fmt.Errorf("failed to write key: %w", err)
	}

	return nil
}

// LoadSecretKey loads the secret key from a file
func LoadSecretKey(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read key: %w", err)
	}

	// Decode from base64
	key, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return nil, fmt.Errorf("failed to decode key: %w", err)
	}

	if len(key) != 32 {
		return nil, fmt.Errorf("invalid key length: %d (expected 32)", len(key))
	}

	return key, nil
}

// GetOrCreateSecretKey loads or generates a secret key
func GetOrCreateSecretKey(path string) ([]byte, error) {
	// Try to load existing key
	key, err := LoadSecretKey(path)
	if err == nil {
		return key, nil
	}

	// Generate new key
	key, err = GenerateSecretKey()
	if err != nil {
		return nil, err
	}

	// Save it
	if err := SaveSecretKey(path, key); err != nil {
		return nil, err
	}

	return key, nil
}

// GenerateTerminalID generates a new UUID-based terminal ID.
func GenerateTerminalID() (string, error) {
	// Generate 16 random bytes for UUID v4
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate terminal ID: %w", err)
	}

	// Set version (4) and variant bits
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant 10

	// Format as UUID string
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

const (
	// terminalIDDirNameBytes is the number of bytes from sha256(workdir) to use
	// as the directory discriminator for terminal ids.
	terminalIDDirNameBytes = 12
	terminalIDFileName     = "terminal.id"
)

// terminalIDPath returns the persisted terminal id path for a given workdir.
//
// We scope terminal ids to a directory so one paired terminal maps to one
// directory. This avoids the "daemon machine with many terminals" pattern and
// makes the authorization boundary explicit.
func terminalIDPath(delightHome string, workDir string) string {
	hash := sha256.Sum256([]byte(workDir))
	dirKey := hex.EncodeToString(hash[:terminalIDDirNameBytes])
	return filepath.Join(delightHome, "terminals", dirKey, terminalIDFileName)
}

// GetOrCreateTerminalID loads or generates a stable terminal id for a workdir.
func GetOrCreateTerminalID(delightHome string, workDir string) (string, error) {
	path := terminalIDPath(delightHome, workDir)

	// Try to load existing ID.
	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id, nil
		}
	}

	// Generate new ID.
	id, err := GenerateTerminalID()
	if err != nil {
		return "", err
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("failed to create terminal id directory: %w", err)
	}

	// Save it with restrictive permissions.
	if err := os.WriteFile(path, []byte(id), 0o600); err != nil {
		return "", fmt.Errorf("failed to save terminal ID: %w", err)
	}
	return id, nil
}
