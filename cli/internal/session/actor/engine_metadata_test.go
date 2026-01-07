package actor

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReduceEngineSessionIdentifiedStoresResumeToken ensures evEngineSessionIdentified updates state.ResumeToken.
func TestReduceEngineSessionIdentifiedStoresResumeToken(t *testing.T) {
	state := State{RunnerGen: 2}

	next, effects := Reduce(state, evEngineSessionIdentified{Gen: 2, ResumeToken: "abc"})
	require.NotNil(t, effects)
	require.NotEmpty(t, effects)
	require.Equal(t, "abc", next.ResumeToken)
	require.Equal(t, "abc", next.ClaudeSessionID)
	require.Equal(t, "abc", next.AgentState.ResumeToken)
}

// TestReduceEngineRolloutPathStoresPath ensures evEngineRolloutPath updates state.RolloutPath.
func TestReduceEngineRolloutPathStoresPath(t *testing.T) {
	state := State{RunnerGen: 2}

	next, effects := Reduce(state, evEngineRolloutPath{Gen: 2, Path: "/tmp/rollout.jsonl"})
	require.Nil(t, effects)
	require.Equal(t, "/tmp/rollout.jsonl", next.RolloutPath)
}

// TestReduceSwitchModeUsesResumeToken ensures effStartRemoteRunner uses state.ResumeToken as resume.
func TestReduceSwitchModeUsesResumeToken(t *testing.T) {
	initial := State{
		FSM:         StateLocalRunning,
		Mode:        ModeLocal,
		RunnerGen:   7,
		ResumeToken: "resume-123",
	}

	next, effects := Reduce(initial, cmdSwitchMode{Target: ModeRemote})
	require.Equal(t, StateRemoteStarting, next.FSM)

	found := false
	for _, eff := range effects {
		start, ok := eff.(effStartRemoteRunner)
		if !ok {
			continue
		}
		require.Equal(t, "resume-123", start.Resume)
		found = true
	}
	require.True(t, found, "expected effStartRemoteRunner")
}

// TestReduceSwitchModeStartsLocalWithRollout ensures effStartLocalRunner receives resume + rollout metadata.
func TestReduceSwitchModeStartsLocalWithRollout(t *testing.T) {
	initial := State{
		FSM:         StateRemoteRunning,
		Mode:        ModeRemote,
		RunnerGen:   3,
		ResumeToken: "resume-123",
		RolloutPath: "/tmp/rollout.jsonl",
	}

	next, effects := Reduce(initial, cmdSwitchMode{Target: ModeLocal})
	require.Equal(t, StateLocalStarting, next.FSM)

	found := false
	for _, eff := range effects {
		start, ok := eff.(effStartLocalRunner)
		if !ok {
			continue
		}
		require.Equal(t, "resume-123", start.Resume)
		require.Equal(t, "/tmp/rollout.jsonl", start.RolloutPath)
		found = true
	}
	require.True(t, found, "expected effStartLocalRunner")
}
