package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGolden_UpdateNewMessage(t *testing.T) {
	raw := readTestdata(t, "update_new_message.json")
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))

	cipher, ok, err := ExtractNewMessageCipher(payload)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "cipher", cipher)
}

func TestGolden_MessageEvent(t *testing.T) {
	raw := readTestdata(t, "message_event.json")
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))

	cipher, localID, ok, err := ExtractMessageCipher(payload)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "cipher", cipher)
	require.Equal(t, "lid-1", localID)
}

func TestGolden_UserTextRecord(t *testing.T) {
	raw := readTestdata(t, "user_text_record.json")

	text, meta, ok, err := ParseUserTextRecord(raw)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "hello", text)
	require.Equal(t, map[string]any{"model": "gpt-5"}, meta)
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", name)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return raw
}
