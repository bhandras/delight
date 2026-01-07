package codexengine

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMarshalAssistantTextRecordWrapsOutputRecord ensures marshalAssistantTextRecord produces an AgentOutputRecord.
func TestMarshalAssistantTextRecordWrapsOutputRecord(t *testing.T) {
	raw, err := marshalAssistantTextRecord("hi", "gpt-5.2-codex")
	require.NoError(t, err)

	var obj map[string]any
	require.NoError(t, json.Unmarshal(raw, &obj))
	require.Equal(t, "agent", obj["role"])

	content, ok := obj["content"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "output", content["type"])
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
