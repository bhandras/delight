package actor

import (
	framework "github.com/bhandras/delight/cli/internal/actor"
)

// SwitchMode returns a command input that requests switching the session to the
// given mode. If reply is non-nil, it is completed when the switch finishes.
func SwitchMode(target Mode, reply chan error) framework.Input {
	return cmdSwitchMode{Target: target, Reply: reply}
}

// RemoteSend returns a command input that sends a user message to the remote
// runner. This is only allowed when the session is in RemoteRunning.
func RemoteSend(text string, meta map[string]any, localID string, reply chan error) framework.Input {
	return cmdRemoteSend{Text: text, Meta: meta, LocalID: localID, Reply: reply}
}

// AbortRemote returns a command input that aborts the remote runner.
func AbortRemote(reply chan error) framework.Input {
	return cmdAbortRemote{Reply: reply}
}

// SubmitPermissionDecision returns a command input that submits a permission
// decision for a pending request.
func SubmitPermissionDecision(requestID string, allow bool, message string, nowMs int64, reply chan error) framework.Input {
	return cmdPermissionDecision{
		RequestID: requestID,
		Allow:     allow,
		Message:   message,
		NowMs:     nowMs,
		Reply:     reply,
	}
}

// Shutdown returns a command input that requests stopping all runtimes.
func Shutdown(reply chan error) framework.Input {
	return cmdShutdown{Reply: reply}
}

// WSConnected returns an event input that indicates the session websocket
// connected.
func WSConnected() framework.Input {
	return evWSConnected{}
}

// WSDisconnected returns an event input that indicates the session websocket
// disconnected.
func WSDisconnected(reason string) framework.Input {
	return evWSDisconnected{Reason: reason}
}

// SessionUpdate returns an event input containing an inbound session-update payload.
func SessionUpdate(data map[string]any) framework.Input {
	return evSessionUpdate{Data: data}
}

// MessageUpdate returns an event input containing an inbound update/message payload.
func MessageUpdate(data map[string]any) framework.Input {
	return evMessageUpdate{Data: data}
}

// Ephemeral returns an event input containing an inbound ephemeral payload.
func Ephemeral(data map[string]any) framework.Input {
	return evEphemeral{Data: data}
}

// PersistAgentState returns a command input that requests persisting the given
// agentState JSON string to the server.
//
// This command is intended for the transitional period where the production
// session manager still owns the canonical `types.AgentState` struct but wants
// to delegate persistence (including version-mismatch handling) to the actor.
func PersistAgentState(agentStateJSON string) framework.Input {
	return cmdPersistAgentState{AgentStateJSON: agentStateJSON}
}

// PersistAgentStateImmediate returns a command input that requests persisting
// the given agentState JSON string immediately (no debounce).
func PersistAgentStateImmediate(agentStateJSON string) framework.Input {
	return cmdPersistAgentStateImmediate{AgentStateJSON: agentStateJSON}
}
