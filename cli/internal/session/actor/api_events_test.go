package actor

import (
	"testing"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/stretchr/testify/require"
)

// TestAPI_EventConstructors ensures the remaining event constructors in api.go
// are covered and return the expected concrete input types.
func TestAPI_EventConstructors(t *testing.T) {
	t.Parallel()

	{
		in := WSConnected()
		_, ok := in.(evWSConnected)
		require.True(t, ok)
	}
	{
		in := WSDisconnected("bye")
		evt, ok := in.(evWSDisconnected)
		require.True(t, ok)
		require.Equal(t, "bye", evt.Reason)
	}
	{
		in := TerminalConnected()
		_, ok := in.(evTerminalConnected)
		require.True(t, ok)
	}
	{
		in := TerminalDisconnected("gone")
		evt, ok := in.(evTerminalDisconnected)
		require.True(t, ok)
		require.Equal(t, "gone", evt.Reason)
	}
	{
		in := SessionUpdate(map[string]any{"x": 1})
		evt, ok := in.(evSessionUpdate)
		require.True(t, ok)
		require.Equal(t, any(1), evt.Data["x"])
	}
	{
		in := MessageUpdate(map[string]any{"y": "z"})
		evt, ok := in.(evMessageUpdate)
		require.True(t, ok)
		require.Equal(t, any("z"), evt.Data["y"])
	}
	{
		in := Ephemeral(map[string]any{"type": "e"})
		evt, ok := in.(evEphemeral)
		require.True(t, ok)
		require.Equal(t, any("e"), evt.Data["type"])
	}
	{
		in := DesktopTakeback()
		_, ok := in.(evDesktopTakeback)
		require.True(t, ok)
	}
}

// TestAPI_CommandConstructors ensures the remaining command constructors in
// api.go are covered and return the expected concrete input types.
func TestAPI_CommandConstructors(t *testing.T) {
	t.Parallel()

	{
		in := PersistAgentState(`{"x":1}`)
		cmd, ok := in.(cmdPersistAgentState)
		require.True(t, ok)
		require.Equal(t, `{"x":1}`, cmd.AgentStateJSON)
	}
	{
		in := PersistAgentStateImmediate(`{"x":2}`)
		cmd, ok := in.(cmdPersistAgentStateImmediate)
		require.True(t, ok)
		require.Equal(t, `{"x":2}`, cmd.AgentStateJSON)
	}
	{
		reply := make(chan error, 1)
		in := WaitForAgentStatePersist(reply)
		cmd, ok := in.(cmdWaitForAgentStatePersist)
		require.True(t, ok)
		require.True(t, cmd.Reply == reply)
	}
	{
		in := SetControlledByUser(true, 123)
		cmd, ok := in.(cmdSetControlledByUser)
		require.True(t, ok)
		require.True(t, cmd.ControlledByUser)
		require.Equal(t, int64(123), cmd.NowMs)
	}

	// Ensure api.go types still satisfy the actor framework input interface.
	var _ = framework.Input(WSConnected())
}
