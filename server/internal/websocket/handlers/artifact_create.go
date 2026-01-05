package handlers

import (
	"context"
	"database/sql"
	"encoding/base64"

	"github.com/bhandras/delight/server/internal/models"
	"github.com/bhandras/delight/shared/logger"
	protocolwire "github.com/bhandras/delight/shared/wire"
)

const (
	// maxWrappedDataKeyBytes caps the size of a wrapped dataEncryptionKey the
	// server will accept. The server treats these keys as opaque bytes and
	// never decrypts them.
	maxWrappedDataKeyBytes = 4096
)

// ArtifactCreate creates a new artifact (or returns an existing one) and emits a
// user-scoped update event on success.
func ArtifactCreate(ctx context.Context, deps Deps, auth AuthContext, req protocolwire.ArtifactCreateRequest) EventResult {
	if req.ID == "" || req.Header == "" || req.Body == "" || req.DataEncryptionKey == "" {
		return NewEventResult(protocolwire.ArtifactAck{Result: "error", Message: "Invalid parameters"}, nil)
	}

	existing, err := deps.Artifacts().GetArtifactByID(ctx, req.ID)
	if err == nil {
		if existing.AccountID != auth.UserID() {
			return NewEventResult(protocolwire.ArtifactAck{
				Result:  "error",
				Message: "Artifact with this ID already exists for another account",
			}, nil)
		}
		return NewEventResult(protocolwire.ArtifactAck{
			Result: "success",
			Artifact: &protocolwire.ArtifactInfo{
				ID:            existing.ID,
				Header:        base64.StdEncoding.EncodeToString(existing.Header),
				HeaderVersion: existing.HeaderVersion,
				Body:          base64.StdEncoding.EncodeToString(existing.Body),
				BodyVersion:   existing.BodyVersion,
				Seq:           existing.Seq,
				CreatedAt:     existing.CreatedAt.UnixMilli(),
				UpdatedAt:     existing.UpdatedAt.UnixMilli(),
			},
		}, nil)
	}
	if err != sql.ErrNoRows {
		return NewEventResult(protocolwire.ArtifactAck{Result: "error", Message: "Internal error"}, nil)
	}

	headerBytes, err := base64.StdEncoding.DecodeString(req.Header)
	if err != nil {
		return NewEventResult(protocolwire.ArtifactAck{Result: "error", Message: "Invalid header encoding"}, nil)
	}
	bodyBytes, err := base64.StdEncoding.DecodeString(req.Body)
	if err != nil {
		return NewEventResult(protocolwire.ArtifactAck{Result: "error", Message: "Invalid body encoding"}, nil)
	}
	dataKeyBytes, err := base64.StdEncoding.DecodeString(req.DataEncryptionKey)
	if err != nil {
		return NewEventResult(protocolwire.ArtifactAck{Result: "error", Message: "Invalid dataEncryptionKey encoding"}, nil)
	}
	if len(dataKeyBytes) == 0 || len(dataKeyBytes) > maxWrappedDataKeyBytes {
		return NewEventResult(protocolwire.ArtifactAck{Result: "error", Message: "Invalid dataEncryptionKey size"}, nil)
	}

	if err := deps.Artifacts().CreateArtifact(ctx, models.CreateArtifactParams{
		ID:                req.ID,
		AccountID:         auth.UserID(),
		Header:            headerBytes,
		HeaderVersion:     1,
		Body:              bodyBytes,
		BodyVersion:       1,
		DataEncryptionKey: dataKeyBytes,
		Seq:               0,
	}); err != nil {
		return NewEventResult(protocolwire.ArtifactAck{Result: "error", Message: "Internal error"}, nil)
	}

	artifact, err := deps.Artifacts().GetArtifactByIDAndAccount(ctx, models.GetArtifactByIDAndAccountParams{
		ID:        req.ID,
		AccountID: auth.UserID(),
	})
	if err != nil {
		return NewEventResult(protocolwire.ArtifactAck{Result: "error", Message: "Internal error"}, nil)
	}

	ack := protocolwire.ArtifactAck{
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
		Body: protocolwire.UpdateBodyNewArtifact{
			T:                 "new-artifact",
			ArtifactID:        artifact.ID,
			Seq:               artifact.Seq,
			Header:            base64.StdEncoding.EncodeToString(artifact.Header),
			HeaderVersion:     artifact.HeaderVersion,
			Body:              base64.StdEncoding.EncodeToString(artifact.Body),
			BodyVersion:       artifact.BodyVersion,
			DataEncryptionKey: base64.StdEncoding.EncodeToString(artifact.DataEncryptionKey),
			CreatedAt:         artifact.CreatedAt.UnixMilli(),
			UpdatedAt:         artifact.UpdatedAt.UnixMilli(),
		},
	}

	return NewEventResult(ack, []UpdateInstruction{newUserUpdate(auth.UserID(), event)})
}
