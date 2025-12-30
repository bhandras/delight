package wire

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeContentBlocks(t *testing.T) {
	blocks, err := DecodeContentBlocks([]any{
		map[string]any{"type": "text", "text": "hello"},
		map[string]any{
			"type":  "tool_use",
			"id":    "t1",
			"name":  "bash",
			"input": map[string]any{"command": "ls"},
		},
	})
	require.NoError(t, err)
	require.Len(t, blocks, 2)
	require.Equal(t, "text", blocks[0].Type)
	require.Equal(t, "hello", blocks[0].Text)
	require.Equal(t, "tool_use", blocks[1].Type)
	require.Empty(t, blocks[1].Text)
	require.Equal(t, "bash", blocks[1].Fields["name"])
	require.Equal(t, "t1", blocks[1].Fields["id"])
}

func TestTryParseAgentOutputRecord(t *testing.T) {
	raw := []byte(`{
  "role": "agent",
  "content": {
    "type": "output",
    "data": {
      "type": "assistant",
      "isSidechain": false,
      "isCompactSummary": false,
      "isMeta": false,
      "uuid": "u1",
      "parentUuid": null,
      "message": {
        "role": "assistant",
        "model": "m1",
        "content": [
          { "type": "text", "text": "hello" },
          {
            "type": "tool_use",
            "id": "t1",
            "name": "bash",
            "input": { "command": "ls" }
          }
        ]
      }
    }
  }
}`)

	rec, ok, err := TryParseAgentOutputRecord(raw)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, rec)
	require.Equal(t, "agent", rec.Role)
	require.Equal(t, "output", rec.Content.Type)
	require.Equal(t, "assistant", rec.Content.Data.Message.Role)
	require.Len(t, rec.Content.Data.Message.Content, 2)
	require.Equal(t, "hello", rec.Content.Data.Message.Content[0].Text)
	require.Equal(t, "bash", rec.Content.Data.Message.Content[1].Fields["name"])
}

func TestTryParseAgentOutputRecord_NonOutput(t *testing.T) {
	rec, ok, err := TryParseAgentOutputRecord([]byte(`{"role":"agent","content":{"type":"codex","data":{}}}`))
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, rec)
}

func TestValidateContentBlocks(t *testing.T) {
	require.Error(t, ValidateContentBlocks(nil))
	require.Error(t, ValidateContentBlocks([]ContentBlock{{Type: ""}}))
	require.NoError(t, ValidateContentBlocks([]ContentBlock{{Type: "text"}}))
}
