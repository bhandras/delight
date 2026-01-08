package rollout

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseLineSessionMeta ensures session_meta lines parse into EvSessionMeta.
func TestParseLineSessionMeta(t *testing.T) {
	line := []byte(`{"timestamp":"2026-01-01T09:59:16.585Z","type":"session_meta","payload":{"id":"019b78ff-4f62-77c3-9901-af79a68020af"}}` + "\n")

	ev, ok, err := ParseLine(line)
	require.NoError(t, err)
	require.True(t, ok)

	meta, ok := ev.(EvSessionMeta)
	require.True(t, ok)
	require.Equal(t, "019b78ff-4f62-77c3-9901-af79a68020af", meta.SessionID)
	require.NotZero(t, meta.AtMs)
}

// TestParseLineUserMessage ensures event_msg user_message lines parse into EvUserMessage.
func TestParseLineUserMessage(t *testing.T) {
	line := []byte(`{"timestamp":"2026-01-01T09:59:16.585Z","type":"event_msg","payload":{"type":"user_message","message":"Reply with just OK.","images":[]}}` + "\n")

	ev, ok, err := ParseLine(line)
	require.NoError(t, err)
	require.True(t, ok)

	msg, ok := ev.(EvUserMessage)
	require.True(t, ok)
	require.Equal(t, "Reply with just OK.", msg.Text)
	require.NotZero(t, msg.AtMs)
}

// TestParseLineAssistantMessageFromResponseItem ensures assistant response_item lines parse into EvAssistantMessage.
func TestParseLineAssistantMessageFromResponseItem(t *testing.T) {
	line := []byte(`{"timestamp":"2026-01-01T09:59:17.432Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}}` + "\n")

	ev, ok, err := ParseLine(line)
	require.NoError(t, err)
	require.True(t, ok)

	msg, ok := ev.(EvAssistantMessage)
	require.True(t, ok)
	require.Equal(t, "OK", msg.Text)
	require.NotZero(t, msg.AtMs)
}

func TestParseLineReasoningSummaryFromResponseItem(t *testing.T) {
	line := []byte(`{"timestamp":"2026-01-01T09:59:17.432Z","type":"response_item","payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"**Plan**\n\nDo the thing."}],"content":null,"encrypted_content":"..."}}` + "\n")

	ev, ok, err := ParseLine(line)
	require.NoError(t, err)
	require.True(t, ok)

	msg, ok := ev.(EvReasoningSummary)
	require.True(t, ok)
	require.Equal(t, "**Plan**\n\nDo the thing.", msg.Text)
	require.NotZero(t, msg.AtMs)
}

func TestParseLineFunctionCallFromResponseItem(t *testing.T) {
	line := []byte(`{"timestamp":"2026-01-01T09:59:17.432Z","type":"response_item","payload":{"type":"function_call","name":"shell_command","arguments":"{\"command\":\"rg -n hello\"}","call_id":"call_123"}}` + "\n")

	ev, ok, err := ParseLine(line)
	require.NoError(t, err)
	require.True(t, ok)

	call, ok := ev.(EvFunctionCall)
	require.True(t, ok)
	require.Equal(t, "call_123", call.CallID)
	require.Equal(t, "shell_command", call.Name)
	require.Equal(t, `{"command":"rg -n hello"}`, call.Arguments)
	require.NotZero(t, call.AtMs)
}

func TestParseLineFunctionCallOutputFromResponseItem(t *testing.T) {
	line := []byte(`{"timestamp":"2026-01-01T09:59:17.432Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_123","output":"Exit code: 0\nOutput:\nhi"}}` + "\n")

	ev, ok, err := ParseLine(line)
	require.NoError(t, err)
	require.True(t, ok)

	out, ok := ev.(EvFunctionCallOutput)
	require.True(t, ok)
	require.Equal(t, "call_123", out.CallID)
	require.Equal(t, "Exit code: 0\nOutput:\nhi", out.Output)
	require.NotZero(t, out.AtMs)
}
