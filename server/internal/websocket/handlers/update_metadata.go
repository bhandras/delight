package handlers

import (
	"context"

	"github.com/bhandras/delight/server/internal/models"
	"github.com/bhandras/delight/shared/logger"
	protocolwire "github.com/bhandras/delight/shared/wire"
)

// UpdateMetadata applies a session metadata update and returns an ACK payload
// plus any resulting update events that should be broadcast.
func UpdateMetadata(ctx context.Context, deps Deps, auth AuthContext, req protocolwire.UpdateMetadataPayload) EventResult {
	if req.SID == "" || req.Metadata == "" {
		return NewEventResult(protocolwire.ResultAck{Result: "error"}, nil)
	}

	session, err := deps.Sessions().GetSessionByID(ctx, req.SID)
	if err != nil || session.AccountID != auth.UserID() {
		return NewEventResult(protocolwire.ResultAck{Result: "error"}, nil)
	}

	rows, err := deps.Sessions().UpdateSessionMetadata(ctx, models.UpdateSessionMetadataParams{
		Metadata:          req.Metadata,
		MetadataVersion:   req.ExpectedVersion + 1,
		ID:                req.SID,
		MetadataVersion_2: req.ExpectedVersion,
	})
	if err != nil || rows == 0 {
		current, err := deps.Sessions().GetSessionByID(ctx, req.SID)
		if err == nil {
			return NewEventResult(protocolwire.VersionedAck{
				Result:   "version-mismatch",
				Version:  current.MetadataVersion,
				Metadata: current.Metadata,
			}, nil)
		}
		return NewEventResult(protocolwire.ResultAck{Result: "error"}, nil)
	}

	ack := protocolwire.VersionedAck{
		Result:   "success",
		Version:  req.ExpectedVersion + 1,
		Metadata: req.Metadata,
	}

	userSeq, err := deps.Accounts().UpdateAccountSeq(ctx, auth.UserID())
	if err != nil {
		logger.Errorf("Failed to allocate user seq: %v", err)
		return NewEventResult(ack, nil)
	}

	event := protocolwire.UpdateEvent{
		ID:        deps.NewID(),
		Seq:       userSeq,
		CreatedAt: deps.Now().UnixMilli(),
		Body: protocolwire.UpdateBodyUpdateSession{
			T:  "update-session",
			ID: req.SID,
			Metadata: &protocolwire.VersionedString{
				Value:   req.Metadata,
				Version: req.ExpectedVersion + 1,
			},
		},
	}

	return NewEventResult(ack, []UpdateInstruction{newSessionUpdateSkippingSelf(auth.UserID(), req.SID, event)})
}
