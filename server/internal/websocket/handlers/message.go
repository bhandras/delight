package handlers

import protocolwire "github.com/bhandras/delight/shared/wire"

// EnqueueMessageInstruction describes a validated message ingest operation.
type EnqueueMessageInstruction struct {
	userID     string
	sessionID  string
	content    string
	localID    *string
	skipSocket string
}

// UserID returns the account id for the message ingest.
func (e EnqueueMessageInstruction) UserID() string { return e.userID }

// SessionID returns the session id to ingest into.
func (e EnqueueMessageInstruction) SessionID() string { return e.sessionID }

// Content returns the ciphertext to ingest.
func (e EnqueueMessageInstruction) Content() string { return e.content }

// LocalID returns the optional idempotency key.
func (e EnqueueMessageInstruction) LocalID() *string { return e.localID }

// SkipSocketID returns the socket id that originated the message.
func (e EnqueueMessageInstruction) SkipSocketID() string { return e.skipSocket }

// MessageIngest validates a "message" event payload and returns an enqueue
// instruction when successful.
func MessageIngest(auth AuthContext, defaultSessionID string, req protocolwire.OutboundMessagePayload) *EnqueueMessageInstruction {
	targetSessionID := req.SID
	if targetSessionID == "" {
		targetSessionID = defaultSessionID
	}
	if targetSessionID == "" {
		return nil
	}
	if req.Message == "" {
		return nil
	}

	var localIDValue *string
	if req.LocalID != "" {
		v := req.LocalID
		localIDValue = &v
	}

	return &EnqueueMessageInstruction{
		userID:     auth.UserID(),
		sessionID:  targetSessionID,
		content:    req.Message,
		localID:    localIDValue,
		skipSocket: auth.SocketID(),
	}
}
