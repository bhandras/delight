package codexengine

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestResolveModeExtractsMeta ensures resolveMode reads permissionMode/model from meta.
func TestResolveModeExtractsMeta(t *testing.T) {
	meta := map[string]any{
		configKeyPermissionMode: "read-only",
		configKeyModel:          "gpt-5.2",
	}

	permissionMode, model, changed := resolveMode(permissionModeDefault, "", meta)
	require.True(t, changed)
	require.Equal(t, "read-only", permissionMode)
	require.Equal(t, "gpt-5.2", model)
}

// TestMarshalCodexRecordWrapsAgentCodexRecord ensures marshalCodexRecord wraps data under the codex record envelope.
func TestMarshalCodexRecordWrapsAgentCodexRecord(t *testing.T) {
	raw, err := marshalCodexRecord(map[string]any{"type": "message", "message": "hi"})
	require.NoError(t, err)

	var obj map[string]any
	require.NoError(t, json.Unmarshal(raw, &obj))
	require.Equal(t, "agent", obj["role"])

	content, ok := obj["content"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "codex", content["type"])
}

// TestMarshalUserTextRecordMatchesWireShape ensures marshalUserTextRecord produces the expected plaintext user record.
func TestMarshalUserTextRecordMatchesWireShape(t *testing.T) {
	raw, err := marshalUserTextRecord("hello", map[string]any{"k": "v"})
	require.NoError(t, err)

	var obj map[string]any
	require.NoError(t, json.Unmarshal(raw, &obj))
	require.Equal(t, "user", obj["role"])
	require.Equal(t, map[string]any{"k": "v"}, obj["meta"])

	content, ok := obj["content"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "text", content["type"])
	require.Equal(t, "hello", content["text"])
}
