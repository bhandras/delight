package actor

import (
	"testing"

	"github.com/bhandras/delight/cli/internal/actor"
)

// TestSessionInputMarkers exercises marker methods used to classify inputs and
// effects. The methods are intentionally no-ops but should remain stable as the
// actor framework evolves.
func TestSessionInputMarkers(t *testing.T) {
	t.Parallel()

	// Events.
	(evWSConnected{}).isSessionEvent()
	(evWSDisconnected{}).isSessionEvent()
	(evTerminalConnected{}).isSessionEvent()
	(evTerminalDisconnected{}).isSessionEvent()
	(evSessionUpdate{}).isSessionEvent()
	(evMessageUpdate{}).isSessionEvent()
	(evEphemeral{}).isSessionEvent()
	(evRunnerReady{}).isSessionEvent()
	(evRunnerExited{}).isSessionEvent()
	(evEngineRolloutPath{}).isSessionEvent()
	(evEngineThinking{}).isSessionEvent()
	(evEngineUIEvent{}).isSessionEvent()
	(evPermissionRequested{}).isSessionEvent()
	(evDesktopTakeback{}).isSessionEvent()
	(evTimerFired{}).isSessionEvent()
	(evOutboundMessageReady{}).isSessionEvent()
	(EvAgentStatePersisted{}).isSessionEvent()
	(EvAgentStateVersionMismatch{}).isSessionEvent()
	(EvAgentStatePersistFailed{}).isSessionEvent()

	// Commands.
	(cmdSwitchMode{}).isSessionCommand()
	(cmdRemoteSend{}).isSessionCommand()
	(cmdInboundUserMessage{}).isSessionCommand()
	(cmdAbortRemote{}).isSessionCommand()
	(cmdPermissionDecision{}).isSessionCommand()
	(cmdPermissionAwait{}).isSessionCommand()
	(cmdShutdown{}).isSessionCommand()
	(cmdPersistAgentState{}).isSessionCommand()
	(cmdPersistAgentStateImmediate{}).isSessionCommand()
	(cmdWaitForAgentStatePersist{}).isSessionCommand()
	(cmdSetControlledByUser{}).isSessionCommand()
	(cmdSetAgentConfig{}).isSessionCommand()
	(cmdGetAgentEngineSettings{}).isSessionCommand()

	// Sanity: ensure event/command types still implement the actor framework
	// input interface.
	var _ actor.Input = evWSConnected{}
	var _ actor.Input = cmdShutdown{}
}
