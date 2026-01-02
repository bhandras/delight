package handlers

import (
	"testing"
	"time"

	protocolwire "github.com/bhandras/delight/shared/wire"
	"github.com/stretchr/testify/require"
)

type fakeRPCMethodLocator struct {
	socketID string
	ok       bool
}

func (f fakeRPCMethodLocator) GetSocketID(userID, method string) (string, bool) {
	return f.socketID, f.ok
}

func TestRPCCall_MissingMethod(t *testing.T) {
	forward, ack := RPCCall(NewAuthContext("u1", "user-scoped", "sock1"), fakeRPCMethodLocator{ok: true, socketID: "sock2"}, protocolwire.RPCCallPayload{})
	require.Nil(t, forward)
	require.NotNil(t, ack)
	require.False(t, ack.OK)
	require.Contains(t, ack.Error, "method")
}

func TestRPCCall_NotAvailable(t *testing.T) {
	forward, ack := RPCCall(NewAuthContext("u1", "user-scoped", "sock1"), fakeRPCMethodLocator{ok: false}, protocolwire.RPCCallPayload{Method: "m"})
	require.Nil(t, forward)
	require.NotNil(t, ack)
	require.False(t, ack.OK)
	require.Contains(t, ack.Error, "not available")
}

func TestRPCCall_SameSocketDisallowed(t *testing.T) {
	forward, ack := RPCCall(NewAuthContext("u1", "user-scoped", "sock1"), fakeRPCMethodLocator{ok: true, socketID: "sock1"}, protocolwire.RPCCallPayload{Method: "m"})
	require.Nil(t, forward)
	require.NotNil(t, ack)
	require.False(t, ack.OK)
	require.Contains(t, ack.Error, "same socket")
}

func TestRPCCall_SuccessReturnsForward(t *testing.T) {
	forward, ack := RPCCall(NewAuthContext("u1", "user-scoped", "sock1"), fakeRPCMethodLocator{ok: true, socketID: "sock2"}, protocolwire.RPCCallPayload{
		Method: "m",
		Params: "p",
	})
	require.Nil(t, ack)
	require.NotNil(t, forward)
	require.Equal(t, "sock2", forward.TargetSocketID())
	require.Equal(t, 30*time.Second, forward.Timeout())
	require.Equal(t, "m", forward.Request().Method)
	require.Equal(t, "p", forward.Request().Params)
}
