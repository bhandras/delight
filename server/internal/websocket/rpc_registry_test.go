package websocket

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRPCRegistry_RegisterLookup(t *testing.T) {
	r := NewRPCRegistry()
	r.Register("u1", "m", "sock1")

	sock, ok := r.GetSocketID("u1", "m")
	require.True(t, ok)
	require.Equal(t, "sock1", sock)
}

func TestRPCRegistry_UnregisterIsSocketScoped(t *testing.T) {
	r := NewRPCRegistry()
	r.Register("u1", "m", "sock1")

	r.Unregister("u1", "m", "other")
	sock, ok := r.GetSocketID("u1", "m")
	require.True(t, ok)
	require.Equal(t, "sock1", sock)

	r.Unregister("u1", "m", "sock1")
	_, ok = r.GetSocketID("u1", "m")
	require.False(t, ok)
}

func TestRPCRegistry_UnregisterAll(t *testing.T) {
	r := NewRPCRegistry()
	r.Register("u1", "m1", "sock1")
	r.Register("u1", "m2", "sock2")
	r.Register("u1", "m3", "sock1")

	r.UnregisterAll("u1", "sock1")

	_, ok := r.GetSocketID("u1", "m1")
	require.False(t, ok)
	_, ok = r.GetSocketID("u1", "m3")
	require.False(t, ok)

	sock, ok := r.GetSocketID("u1", "m2")
	require.True(t, ok)
	require.Equal(t, "sock2", sock)
}
