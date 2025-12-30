package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	protocolwire "github.com/bhandras/delight/protocol/wire"
	"github.com/bhandras/delight/server/internal/models"
	pkgtypes "github.com/bhandras/delight/server/pkg/types"
)

// SQLStore implements Store on top of sqlc queries.
type SQLStore struct {
	Queries *models.Queries
}

func (s *SQLStore) ValidateSessionOwner(ctx context.Context, sessionID, userID string) (bool, error) {
	session, err := s.Queries.GetSessionByID(ctx, sessionID)
	if err != nil {
		return false, err
	}
	return session.AccountID == userID, nil
}

func (s *SQLStore) NextSessionMessageSeq(ctx context.Context, sessionID string) (int64, error) {
	return s.Queries.UpdateSessionSeq(ctx, sessionID)
}

func (s *SQLStore) CreateEncryptedMessage(ctx context.Context, sessionID string, seq int64, localID *string, cipher string) (StoredMessage, error) {
	var local sql.NullString
	if localID != nil && *localID != "" {
		local = sql.NullString{String: *localID, Valid: true}
	}
	env, err := json.Marshal(protocolwire.EncryptedEnvelope{T: "encrypted", C: cipher})
	if err != nil {
		return StoredMessage{}, err
	}
	msg, err := s.Queries.CreateMessage(ctx, models.CreateMessageParams{
		ID:        pkgtypes.NewCUID(),
		SessionID: sessionID,
		LocalID:   local,
		Seq:       seq,
		Content:   string(env),
	})
	if err != nil {
		return StoredMessage{}, err
	}
	return StoredMessage{
		ID:        msg.ID,
		Seq:       msg.Seq,
		CreatedAt: msg.CreatedAt.UnixMilli(),
		UpdatedAt: msg.UpdatedAt.UnixMilli(),
	}, nil
}

func (s *SQLStore) MarkSessionActive(ctx context.Context, sessionID string) error {
	return s.Queries.UpdateSessionActivity(ctx, models.UpdateSessionActivityParams{
		Active:       1,
		LastActiveAt: time.Now(),
		ID:           sessionID,
	})
}

func (s *SQLStore) NextUserSeq(ctx context.Context, userID string) (int64, error) {
	return s.Queries.UpdateAccountSeq(ctx, userID)
}
