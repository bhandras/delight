package handlers

import (
	"context"

	"github.com/bhandras/delight/server/internal/models"
	"github.com/bhandras/delight/shared/logger"
	protocolwire "github.com/bhandras/delight/shared/wire"
)

// TerminalUpdateMetadata applies a terminal metadata update and returns an ACK
// payload plus any resulting update events that should be broadcast.
func TerminalUpdateMetadata(ctx context.Context, deps Deps, auth AuthContext, req protocolwire.TerminalUpdateMetadataPayload) EventResult {
	if req.TerminalID == "" || req.Metadata == "" {
		return NewEventResult(protocolwire.ResultAck{Result: "error", Message: "Invalid parameters"}, nil)
	}

	terminal, err := deps.Terminals().GetTerminal(ctx, models.GetTerminalParams{
		AccountID: auth.UserID(),
		ID:        req.TerminalID,
	})
	if err != nil || terminal.AccountID != auth.UserID() {
		return NewEventResult(protocolwire.ResultAck{Result: "error", Message: "Terminal not found"}, nil)
	}

	if terminal.MetadataVersion != req.ExpectedVersion {
		return NewEventResult(protocolwire.VersionedAck{
			Result:   "version-mismatch",
			Version:  terminal.MetadataVersion,
			Metadata: terminal.Metadata,
		}, nil)
	}

	rows, err := deps.Terminals().UpdateTerminalMetadata(ctx, models.UpdateTerminalMetadataParams{
		Metadata:          req.Metadata,
		MetadataVersion:   req.ExpectedVersion + 1,
		AccountID:         auth.UserID(),
		ID:                req.TerminalID,
		MetadataVersion_2: req.ExpectedVersion,
	})
	if err != nil || rows == 0 {
		current, err := deps.Terminals().GetTerminal(ctx, models.GetTerminalParams{
			AccountID: auth.UserID(),
			ID:        req.TerminalID,
		})
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
		Body: protocolwire.UpdateBodyUpdateTerminal{
			T:          "update-terminal",
			TerminalID: req.TerminalID,
			Metadata: &protocolwire.VersionedString{
				Value:   req.Metadata,
				Version: req.ExpectedVersion + 1,
			},
		},
	}

	return NewEventResult(ack, []UpdateInstruction{newUserUpdateSkippingSelf(auth.UserID(), event)})
}
