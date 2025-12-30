package runtime

import "context"

// UpdateEmitter delivers updates to connected clients.
type UpdateEmitter interface {
	EmitUpdateToSession(userID, sessionID string, payload any, skipSocketID string)
}

// Store abstracts persistence for the runtime.
type Store interface {
	ValidateSessionOwner(ctx context.Context, sessionID, userID string) (bool, error)
	NextSessionMessageSeq(ctx context.Context, sessionID string) (int64, error)
	CreateEncryptedMessage(ctx context.Context, sessionID string, seq int64, localID *string, cipher string) (StoredMessage, error)
	MarkSessionActive(ctx context.Context, sessionID string) error
	NextUserSeq(ctx context.Context, userID string) (int64, error)
}

// StoredMessage is a minimal view of a persisted session message.
type StoredMessage struct {
	ID        string
	Seq       int64
	CreatedAt int64
	UpdatedAt int64
}

type messageEvent struct {
	ctx          context.Context
	userID       string
	sessionID    string
	cipher       string
	localID      *string
	skipSocketID string
}
