package crypto

import (
	"crypto/rand"
	"fmt"
)

// RandBytes fills the provided slice with cryptographically secure random
// bytes.
func RandBytes(out []byte) ([]byte, error) {
	if len(out) == 0 {
		return out, fmt.Errorf("output slice is empty")
	}
	if _, err := rand.Read(out); err != nil {
		return nil, fmt.Errorf("rand read: %w", err)
	}
	return out, nil
}

