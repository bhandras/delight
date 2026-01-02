package handlers

import (
	"context"
	"encoding/base64"

	"github.com/bhandras/delight/server/internal/models"
	protocolwire "github.com/bhandras/delight/shared/wire"
)

// ArtifactRead returns the artifact object for a single artifact id.
func ArtifactRead(ctx context.Context, deps Deps, auth AuthContext, req protocolwire.ArtifactReadRequest) EventResult {
	if req.ArtifactID == "" {
		return NewEventResult(protocolwire.ArtifactAck{Result: "error", Message: "Invalid parameters"}, nil)
	}

	artifact, err := deps.Artifacts().GetArtifactByIDAndAccount(ctx, models.GetArtifactByIDAndAccountParams{
		ID:        req.ArtifactID,
		AccountID: auth.UserID(),
	})
	if err != nil {
		return NewEventResult(protocolwire.ArtifactAck{Result: "error", Message: "Artifact not found"}, nil)
	}

	return NewEventResult(protocolwire.ArtifactAck{
		Result: "success",
		Artifact: &protocolwire.ArtifactInfo{
			ID:            artifact.ID,
			Header:        base64.StdEncoding.EncodeToString(artifact.Header),
			HeaderVersion: artifact.HeaderVersion,
			Body:          base64.StdEncoding.EncodeToString(artifact.Body),
			BodyVersion:   artifact.BodyVersion,
			Seq:           artifact.Seq,
			CreatedAt:     artifact.CreatedAt.UnixMilli(),
			UpdatedAt:     artifact.UpdatedAt.UnixMilli(),
		},
	}, nil)
}
