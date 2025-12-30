package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGolden_UpdateNewMessage(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{
			name: "hand_authored",
			raw:  readTestdata(t, "update_new_message.json"),
		},
		{
			name: "captured",
			raw:  readCaptured(t, "session_update_event_new_message.json"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var payload map[string]any
			require.NoError(t, json.Unmarshal(tt.raw, &payload))

			cipher, ok, err := ExtractNewMessageCipher(payload)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, "cipher", cipher)
		})
	}
}

func TestGolden_MessageEvent(t *testing.T) {
	tests := []struct {
		name        string
		raw         []byte
		wantLocalID string
	}{
		{
			name:        "hand_authored",
			raw:         readTestdata(t, "message_event.json"),
			wantLocalID: "lid-1",
		},
		{
			name:        "captured",
			raw:         readCaptured(t, "session_message_event.json"),
			wantLocalID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var payload map[string]any
			require.NoError(t, json.Unmarshal(tt.raw, &payload))

			cipher, localID, ok, err := ExtractMessageCipher(payload)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, "cipher", cipher)
			require.Equal(t, tt.wantLocalID, localID)
		})
	}
}

func TestGolden_UpdateEnvelope_NonMessageTypes(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{
			name: "update_machine_daemon_state",
			raw:  readCaptured(t, "session_update_event_update_machine_daemon_state.json"),
		},
		{
			name: "update_machine_metadata",
			raw:  readCaptured(t, "session_update_event_update_machine_metadata.json"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var payload map[string]any
			require.NoError(t, json.Unmarshal(tt.raw, &payload))

			cipher, ok, err := ExtractNewMessageCipher(payload)
			require.NoError(t, err)
			require.False(t, ok)
			require.Empty(t, cipher)
		})
	}
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

func readCaptured(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", "captured", name)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return raw
}
