package handlers

import protocolwire "github.com/bhandras/delight/protocol/wire"

// UpdateScope describes where an update should be emitted.
type UpdateScope int

const (
	updateScopeUnknown UpdateScope = iota
	updateScopeUser
	updateScopeSession
)

// UpdateInstruction describes a single outbound update emission produced by a
// handler call.
type UpdateInstruction struct {
	scope     UpdateScope
	userID    string
	sessionID string
	event     protocolwire.UpdateEvent
	skipSelf  bool
}

func newUserUpdate(userID string, event protocolwire.UpdateEvent) UpdateInstruction {
	return UpdateInstruction{scope: updateScopeUser, userID: userID, event: event}
}

func newUserUpdateSkippingSelf(userID string, event protocolwire.UpdateEvent) UpdateInstruction {
	return UpdateInstruction{scope: updateScopeUser, userID: userID, event: event, skipSelf: true}
}

func newSessionUpdate(userID, sessionID string, event protocolwire.UpdateEvent) UpdateInstruction {
	return UpdateInstruction{scope: updateScopeSession, userID: userID, sessionID: sessionID, event: event}
}

func newSessionUpdateSkippingSelf(userID, sessionID string, event protocolwire.UpdateEvent) UpdateInstruction {
	return UpdateInstruction{scope: updateScopeSession, userID: userID, sessionID: sessionID, event: event, skipSelf: true}
}

// Scope returns where the update should be emitted.
func (u UpdateInstruction) Scope() UpdateScope { return u.scope }

// IsUser reports whether the update should be emitted to all user sockets.
func (u UpdateInstruction) IsUser() bool { return u.scope == updateScopeUser }

// IsSession reports whether the update should be emitted to session-scoped
// sockets for the given session id (plus user-scoped sockets).
func (u UpdateInstruction) IsSession() bool { return u.scope == updateScopeSession }

// SkipSelf reports whether the transport adapter should skip emitting the update
// back to the calling socket.
func (u UpdateInstruction) SkipSelf() bool { return u.skipSelf }

// UserID returns the account id for the emission.
func (u UpdateInstruction) UserID() string { return u.userID }

// SessionID returns the target session id for session-scoped emissions.
func (u UpdateInstruction) SessionID() string { return u.sessionID }

// Event returns the update event payload.
func (u UpdateInstruction) Event() protocolwire.UpdateEvent { return u.event }

// EventResult is the output of a handler invocation.
type EventResult struct {
	ack     any
	updates []UpdateInstruction
}

// NewEventResult constructs a handler result.
func NewEventResult(ack any, updates []UpdateInstruction) EventResult {
	return EventResult{ack: ack, updates: updates}
}

// Ack returns the ACK payload to send to the caller.
func (r EventResult) Ack() any { return r.ack }

// Updates returns the list of update emissions requested by the handler.
func (r EventResult) Updates() []UpdateInstruction { return r.updates }
