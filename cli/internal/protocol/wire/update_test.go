package wire

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractNewMessageCipher(t *testing.T) {
	cipher, ok, err := ExtractNewMessageCipher(map[string]any{
		"body": map[string]any{
			"t": "new-message",
			"message": map[string]any{
				"content": map[string]any{
					"c": "ciphertext",
				},
			},
		},
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "ciphertext", cipher)
}

func TestExtractNewMessageCipher_IgnoresOtherTypes(t *testing.T) {
	_, ok, err := ExtractNewMessageCipher(map[string]any{
		"body": map[string]any{
			"t": "something-else",
		},
	})
	require.NoError(t, err)
	require.False(t, ok)
}
