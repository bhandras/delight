package websocket

import (
	"testing"

	"github.com/stretchr/testify/require"
	socket "github.com/zishang520/socket.io/servers/socket/v3"
)

func TestGetFirstAnyWithAck_FuncAck(t *testing.T) {
	var got []any
	payload, ack := getFirstAnyWithAck([]any{
		map[string]any{"k": "v"},
		func(args ...any) { got = args },
	})

	require.Equal(t, map[string]any{"k": "v"}, payload)
	require.NotNil(t, ack)

	ack("a", 1)
	require.Equal(t, []any{"a", 1}, got)
}

func TestGetFirstAnyWithAck_SocketAck(t *testing.T) {
	var gotArgs []any
	var gotErr error

	payload, ack := getFirstAnyWithAck([]any{
		"payload",
		socket.Ack(func(args []any, err error) {
			gotArgs = args
			gotErr = err
		}),
	})

	require.Equal(t, "payload", payload)
	require.NotNil(t, ack)

	ack("x", 2)
	require.Equal(t, []any{"x", 2}, gotArgs)
	require.NoError(t, gotErr)
}

func TestGetFirstAnyWithAck_NoAck(t *testing.T) {
	payload, ack := getFirstAnyWithAck([]any{"payload"})
	require.Equal(t, "payload", payload)
	require.Nil(t, ack)
}
