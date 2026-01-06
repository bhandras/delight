package actor

import (
	"testing"
	"time"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/agentengine"
	"github.com/stretchr/testify/require"
)

func TestRuntime_HandleEngineEvent_OutboundRecordEmitsEncryptedMessage(t *testing.T) {
	t.Parallel()

	rt := NewRuntime(t.TempDir(), false).WithAgent(string(agentengine.AgentFake)).WithEncryptFn(encryptPassthrough)

	// Suppress terminal printing paths while still exercising encryption + emission.
	rt.mu.Lock()
	rt.engineRemoteGen = 9
	rt.engineLocalInteractive = true
	rt.mu.Unlock()

	var got framework.Input
	rt.handleEngineEvent(agentengine.EvOutboundRecord{
		Mode:               agentengine.ModeRemote,
		LocalID:            "local-1",
		Payload:            []byte(`{"role":"user","content":{"type":"text","text":"hi"}}`),
		UserTextNormalized: "hi",
		AtMs:               0,
	}, func(in framework.Input) { got = in })

	ready, ok := got.(evOutboundMessageReady)
	require.True(t, ok, "got=%T", got)
	require.Equal(t, int64(9), ready.Gen)
	require.Equal(t, "local-1", ready.LocalID)
	require.NotEmpty(t, ready.Ciphertext)
	require.NotZero(t, ready.NowMs)

	// Sanity: when AtMs is unset, runtime uses wall clock.
	require.Greater(t, ready.NowMs, int64(0))
	_ = time.UnixMilli(ready.NowMs) // ensure it is a valid millis timestamp
}

func TestRuntime_HandleEngineEvent_LocalReadyMarksInteractive(t *testing.T) {
	t.Parallel()

	rt := NewRuntime(t.TempDir(), false).WithAgent(string(agentengine.AgentFake))

	rt.mu.Lock()
	rt.engineLocalGen = 3
	rt.engineLocalInteractive = false
	rt.mu.Unlock()

	var got framework.Input
	rt.handleEngineEvent(agentengine.EvReady{Mode: agentengine.ModeLocal}, func(in framework.Input) { got = in })

	evt, ok := got.(evRunnerReady)
	require.True(t, ok, "got=%T", got)
	require.Equal(t, ModeLocal, evt.Mode)
	require.Equal(t, int64(3), evt.Gen)

	rt.mu.Lock()
	defer rt.mu.Unlock()
	require.True(t, rt.engineLocalInteractive)
}
