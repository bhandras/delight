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

// InboundUserMessage returns a command input representing a user message
// received from the server (typically originating from a mobile client).
func InboundUserMessage(text string, meta map[string]any, localID string, nowMs int64) framework.Input {
	return cmdInboundUserMessage{Text: text, Meta: meta, LocalID: localID, NowMs: nowMs}
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

// AwaitPermission returns a command input that registers a permission request
// (durably) and returns a decision via the provided reply channel.
//
// This is intended for synchronous runners (e.g. ACP/Codex) that must pause
// until a mobile client responds, without blocking the actor loop.
func AwaitPermission(requestID string, toolName string, inputJSON []byte, nowMs int64, reply chan PermissionDecision) framework.Input {
	return cmdPermissionAwait{
		RequestID: requestID,
		ToolName:  toolName,
		Input:     append([]byte(nil), inputJSON...),
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

// TerminalConnected returns an event input that indicates the terminal websocket
// connected.
func TerminalConnected() framework.Input {
	return evTerminalConnected{}
}

// TerminalDisconnected returns an event input that indicates the terminal
// websocket disconnected.
func TerminalDisconnected(reason string) framework.Input {
	return evTerminalDisconnected{Reason: reason}
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

// DesktopTakeback returns an event input indicating the desktop requested a
// takeback (switch remote -> local).
func DesktopTakeback() framework.Input {
	return evDesktopTakeback{}
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

// WaitForAgentStatePersist returns a command input that forces the current
// agent state to be persisted immediately and completes reply once persistence
// succeeds or fails.
func WaitForAgentStatePersist(reply chan error) framework.Input {
	return cmdWaitForAgentStatePersist{Reply: reply}
}

// SetControlledByUser returns a command input that updates AgentState's
// ControlledByUser flag without changing runner lifecycle.
func SetControlledByUser(controlledByUser bool, nowMs int64) framework.Input {
	return cmdSetControlledByUser{ControlledByUser: controlledByUser, NowMs: nowMs}
}

// SetAgentConfig returns a command input that updates durable agent settings
// (model, permission mode, reasoning effort).
//
// This command is remote-only: callers should enforce that the phone controls
// the session before enqueueing it.
func SetAgentConfig(model string, permissionMode string, reasoningEffort string, reply chan error) framework.Input {
	return cmdSetAgentConfig{
		Model:           model,
		PermissionMode:  permissionMode,
		ReasoningEffort: reasoningEffort,
		Reply:           reply,
	}
}

// GetAgentEngineSettings returns a command input that queries the agent engine
// for its supported settings and current configuration (best-effort).
func GetAgentEngineSettings(reply chan AgentEngineSettingsSnapshot) framework.Input {
	return cmdGetAgentEngineSettings{Reply: reply}
}
