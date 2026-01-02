package handlers

import (
	"context"

	"github.com/bhandras/delight/protocol/logger"
	protocolwire "github.com/bhandras/delight/protocol/wire"
	"github.com/bhandras/delight/server/internal/models"
)

// ArtifactDelete deletes a single artifact and emits a user-scoped update on
// success.
func ArtifactDelete(ctx context.Context, deps Deps, auth AuthContext, req protocolwire.ArtifactDeleteRequest) EventResult {
	if req.ArtifactID == "" {
		return NewEventResult(protocolwire.ArtifactAck{Result: "error", Message: "Invalid parameters"}, nil)
	}

	_, err := deps.Artifacts().GetArtifactByIDAndAccount(ctx, models.GetArtifactByIDAndAccountParams{
		ID:        req.ArtifactID,
		AccountID: auth.UserID(),
	})
	if err != nil {
		return NewEventResult(protocolwire.ArtifactAck{Result: "error", Message: "Artifact not found"}, nil)
	}

	if err := deps.Artifacts().DeleteArtifact(ctx, models.DeleteArtifactParams{
		ID:        req.ArtifactID,
		AccountID: auth.UserID(),
	}); err != nil {
		return NewEventResult(protocolwire.ArtifactAck{Result: "error", Message: "Internal error"}, nil)
	}

	ack := protocolwire.ResultAck{Result: "success"}

	userSeq, err := deps.Accounts().UpdateAccountSeq(ctx, auth.UserID())
	if err != nil {
		logger.Errorf("Failed to allocate user seq: %v", err)
		return NewEventResult(ack, nil)
	}

	event := protocolwire.UpdateEvent{
		ID:        deps.NewID(),
		Seq:       userSeq,
		CreatedAt: deps.Now().UnixMilli(),
		Body: protocolwire.UpdateBodyDeleteArtifact{
			T:          "delete-artifact",
			ArtifactID: req.ArtifactID,
		},
	}

	return NewEventResult(ack, []UpdateInstruction{newUserUpdate(auth.UserID(), event)})
}
