package sdk

import (
	"encoding/base64"
	"testing"

	"github.com/bhandras/delight/cli/internal/crypto"
)

func TestParseTerminalURL(t *testing.T) {
	pub, _, err := crypto.GenerateBoxKeyPair()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub[:])
	pubURL := base64.RawURLEncoding.EncodeToString(pub[:])

	got, err := ParseTerminalURL("delight://terminal?" + pubURL)
	if err != nil {
		t.Fatalf("parse delight: %v", err)
	}
	if got != pubB64 {
		t.Fatalf("unexpected key: %s", got)
	}

	got, err = ParseTerminalURL("happy://terminal?" + pubURL)
	if err != nil {
		t.Fatalf("parse happy: %v", err)
	}
	if got != pubB64 {
		t.Fatalf("unexpected key: %s", got)
	}
}

func TestApproveTerminalAuthEncrypts(t *testing.T) {
	pub, priv, err := crypto.GenerateBoxKeyPair()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	master, err := GenerateMasterKeyBase64()
	if err != nil {
		t.Fatalf("master key: %v", err)
	}
	masterRaw, _ := base64.StdEncoding.DecodeString(master)

	encrypted, err := crypto.EncryptBox(masterRaw, pub)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	decrypted, err := crypto.DecryptBox(encrypted, priv)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if base64.StdEncoding.EncodeToString(decrypted) != master {
		t.Fatalf("roundtrip mismatch")
	}
}
