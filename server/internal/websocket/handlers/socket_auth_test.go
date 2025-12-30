package handlers

import (
	"testing"

	protocolwire "github.com/bhandras/delight/protocol/wire"
	"github.com/stretchr/testify/require"
)

func TestValidateSocketAuthPayload_DefaultClientType(t *testing.T) {
	hs, err := ValidateSocketAuthPayload(protocolwire.SocketAuthPayload{
		Token: "t",
	})
	require.NoError(t, err)
	require.Equal(t, "user-scoped", hs.ClientType)
}

func TestValidateSocketAuthPayload_SessionScopedRequiresSessionID(t *testing.T) {
	_, err := ValidateSocketAuthPayload(protocolwire.SocketAuthPayload{
		Token:      "t",
		ClientType: "session-scoped",
	})
	require.Error(t, err)
}

func TestValidateSocketAuthPayload_MachineScopedRequiresMachineID(t *testing.T) {
	_, err := ValidateSocketAuthPayload(protocolwire.SocketAuthPayload{
		Token:      "t",
		ClientType: "machine-scoped",
	})
	require.Error(t, err)
}

func TestValidateSocketAuthPayload_InvalidClientType(t *testing.T) {
	_, err := ValidateSocketAuthPayload(protocolwire.SocketAuthPayload{
		Token:      "t",
		ClientType: "weird",
	})
	require.Error(t, err)
}
