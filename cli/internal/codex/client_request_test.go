package codex

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error { return nil }

func TestHandleRequest_ElicitationCreate_PatchApproval(t *testing.T) {
	var out bytes.Buffer

	var gotRequestID string
	var gotToolName string
	client := &Client{
		stdin:   nopWriteCloser{Writer: &out},
		pending: make(map[int64]chan rpcResponse),
	}
	client.SetPermissionHandler(func(requestID string, toolName string, input map[string]interface{}) (*PermissionDecision, error) {
		gotRequestID = requestID
		gotToolName = toolName
		return &PermissionDecision{Decision: codexDecisionApproved, Message: ""}, nil
	})

	msg := &rpcMessage{
		JSONRPC: "2.0",
		ID:      0,
		Method:  serverMethodElicitationCreate,
		Params: map[string]interface{}{
			"codex_call_id":          "call_test",
			"codex_elicitation":      codexElicitationPatchApproval,
			"codex_changes":          map[string]interface{}{"foo.txt": map[string]interface{}{"type": "add", "content": "hi"}},
			"codex_event_id":         "evt1",
			"codex_mcp_tool_call_id": "tool1",
		},
	}

	client.handleRequest(msg)

	require.Equal(t, "call_test", gotRequestID)
	require.Equal(t, codexToolApplyPatch, gotToolName)

	lines := bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n"))
	require.Len(t, lines, 1)

	var resp rpcMessage
	require.NoError(t, json.Unmarshal(lines[0], &resp))
	require.Equal(t, "2.0", resp.JSONRPC)
	require.Equal(t, float64(0), resp.ID)
	require.NotNil(t, resp.Result)
	require.Equal(t, codexDecisionApproved, resp.Result["decision"])
}
