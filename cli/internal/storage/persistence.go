package storage

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
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

// GenerateMachineID generates a new UUID-based machine ID
func GenerateMachineID() (string, error) {
	// Generate 16 random bytes for UUID v4
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate machine ID: %w", err)
	}

	// Set version (4) and variant bits
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant 10

	// Format as UUID string
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// GetOrCreateMachineID loads or generates a stable machine ID
func GetOrCreateMachineID(path string) (string, error) {
	// Try to load existing ID
	data, err := os.ReadFile(path)
	if err == nil {
		return string(data), nil
	}

	// Generate new ID
	id, err := GenerateMachineID()
	if err != nil {
		return "", err
	}

	// Save it with restrictive permissions
	if err := os.WriteFile(path, []byte(id), 0600); err != nil {
		return "", fmt.Errorf("failed to save machine ID: %w", err)
	}

	return id, nil
}
