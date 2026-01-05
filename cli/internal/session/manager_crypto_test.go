package session

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/bhandras/delight/cli/internal/crypto"
	"github.com/stretchr/testify/require"
)

func TestSetSessionDataEncryptionKey_WrappedKey(t *testing.T) {
	expected := make([]byte, dataEncryptionKeyBytes)
	for i := range expected {
		expected[i] = byte(i)
	}
	masterSecret := make([]byte, dataEncryptionKeyBytes)
	for i := range masterSecret {
		masterSecret[i] = byte(100 + i)
	}
	encoded, err := crypto.EncryptDataEncryptionKey(expected, masterSecret)
	require.NoError(t, err)

	m := &Manager{masterSecret: masterSecret}
	require.NoError(t, m.setSessionDataEncryptionKey(encoded))
	require.Equal(t, expected, m.dataKey)
}

func TestDecrypt_AESGCMRequiresDataKey(t *testing.T) {
	dataKey := make([]byte, dataEncryptionKeyBytes)
	for i := range dataKey {
		dataKey[i] = byte(i + 10)
	}

	plaintext := json.RawMessage(`{"role":"user","content":{"type":"text","text":"hello"}}`)
	encrypted, err := crypto.EncryptWithDataKey(plaintext, dataKey)
	require.NoError(t, err)
	encoded := base64.StdEncoding.EncodeToString(encrypted)

	m := &Manager{masterSecret: []byte("01234567890123456789012345678901")}
	_, err = m.decrypt(encoded)
	require.Error(t, err)

	m.dataKey = dataKey
	decrypted, err := m.decrypt(encoded)
	require.NoError(t, err)
	require.Equal(t, []byte(plaintext), decrypted)
}
