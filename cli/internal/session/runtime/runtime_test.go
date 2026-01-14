package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRuntime_SetWorkingEmitsActivity(t *testing.T) {
	rt := New(Config{SessionID: "s1"})
	rt.Start()
	t.Cleanup(rt.Stop)

	now := time.Now().UnixMilli()
	require.True(t, rt.Post(SetWorkingEvent{Working: true, AtMs: now}))

	select {
	case cmd := <-rt.Commands():
		activity, ok := cmd.(EmitActivityCommand)
		require.True(t, ok)
		require.Equal(t, "s1", activity.SessionID)
		require.True(t, activity.Working)
		require.True(t, activity.Active)
		require.Equal(t, now, activity.ActiveAtMs)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for command")
	}
}

func TestRuntime_SetWorkingNoopDoesNotEmit(t *testing.T) {
	rt := New(Config{SessionID: "s1"})
	rt.Start()
	t.Cleanup(rt.Stop)

	now := time.Now().UnixMilli()
	require.True(t, rt.Post(SetWorkingEvent{Working: false, AtMs: now}))

	select {
	case <-rt.Commands():
		t.Fatal("unexpected command")
	case <-time.After(50 * time.Millisecond):
	}
}
