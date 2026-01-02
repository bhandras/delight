package crypto

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/curve25519"
)

// DeriveKey matches the JS deriveKey() implementation.
// It derives a 32-byte key using HMAC-SHA512 with a usage string and a path.
func DeriveKey(master []byte, usage string, path []string) ([]byte, error) {
	key, chain, err := deriveSecretKeyTreeRoot(master, usage)
	if err != nil {
		return nil, err
	}
	for _, index := range path {
		key, chain, err = deriveSecretKeyTreeChild(chain, index)
		if err != nil {
			return nil, err
		}
	}
	return key, nil
}

func deriveSecretKeyTreeRoot(seed []byte, usage string) ([]byte, []byte, error) {
	h := hmac.New(sha512.New, []byte(usage+" Master Seed"))
	if _, err := h.Write(seed); err != nil {
		return nil, nil, err
	}
	sum := h.Sum(nil)
	return sum[:32], sum[32:], nil
}

func deriveSecretKeyTreeChild(chainCode []byte, index string) ([]byte, []byte, error) {
	data := append([]byte{0x00}, []byte(index)...)
	h := hmac.New(sha512.New, chainCode)
	if _, err := h.Write(data); err != nil {
		return nil, nil, err
	}
	sum := h.Sum(nil)
	return sum[:32], sum[32:], nil
}

// DeriveContentKeyPair derives the content keypair from the master secret.
// This mirrors Encryption.create() in the JS client.
func DeriveContentKeyPair(master []byte) (*[32]byte, *[32]byte, error) {
	seed, err := DeriveKey(master, "Delight EnCoder", []string{"content"})
	if err != nil {
		return nil, nil, err
	}
	if len(seed) != 32 {
		return nil, nil, fmt.Errorf("invalid content seed length: %d", len(seed))
	}

	var priv [32]byte
	copy(priv[:], seed)

	pubBytes, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to derive public key: %w", err)
	}
	var pub [32]byte
	copy(pub[:], pubBytes)

	return &pub, &priv, nil
}

// DecryptDataEncryptionKey decrypts the session/machine dataEncryptionKey
// using the derived content keypair (box encryption, versioned with 0x00).
func DecryptDataEncryptionKey(encryptedB64 string, master []byte) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encryptedB64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode data key: %w", err)
	}
	if len(raw) < 2 || raw[0] != 0x00 {
		return nil, fmt.Errorf("unsupported data key format")
	}

	_, priv, err := DeriveContentKeyPair(master)
	if err != nil {
		return nil, err
	}

	decrypted, err := DecryptBox(raw[1:], priv)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt data key: %w", err)
	}
	if len(decrypted) != 32 {
		return nil, fmt.Errorf("invalid data key length: %d", len(decrypted))
	}
	return decrypted, nil
}
