package handlers

import (
	"context"

	"github.com/bhandras/delight/server/internal/models"
	"github.com/bhandras/delight/shared/logger"
	protocolwire "github.com/bhandras/delight/shared/wire"
)

// MachineUpdateMetadata applies a machine metadata update and returns an ACK
// payload plus any resulting update events that should be broadcast.
func MachineUpdateMetadata(ctx context.Context, deps Deps, auth AuthContext, req protocolwire.MachineUpdateMetadataPayload) EventResult {
	if req.MachineID == "" || req.Metadata == "" {
		return NewEventResult(protocolwire.ResultAck{Result: "error", Message: "Invalid parameters"}, nil)
	}

	machine, err := deps.Machines().GetMachine(ctx, models.GetMachineParams{
		AccountID: auth.UserID(),
		ID:        req.MachineID,
	})
	if err != nil || machine.AccountID != auth.UserID() {
		return NewEventResult(protocolwire.ResultAck{Result: "error", Message: "Machine not found"}, nil)
	}

	if machine.MetadataVersion != req.ExpectedVersion {
		return NewEventResult(protocolwire.VersionedAck{
			Result:   "version-mismatch",
			Version:  machine.MetadataVersion,
			Metadata: machine.Metadata,
		}, nil)
	}

	rows, err := deps.Machines().UpdateMachineMetadata(ctx, models.UpdateMachineMetadataParams{
		Metadata:          req.Metadata,
		MetadataVersion:   req.ExpectedVersion + 1,
		AccountID:         auth.UserID(),
		ID:                req.MachineID,
		MetadataVersion_2: req.ExpectedVersion,
	})
	if err != nil || rows == 0 {
		current, err := deps.Machines().GetMachine(ctx, models.GetMachineParams{
			AccountID: auth.UserID(),
			ID:        req.MachineID,
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
		Body: protocolwire.UpdateBodyUpdateMachine{
			T:         "update-machine",
			MachineID: req.MachineID,
			Metadata: &protocolwire.VersionedString{
				Value:   req.Metadata,
				Version: req.ExpectedVersion + 1,
			},
		},
	}

	return NewEventResult(ack, []UpdateInstruction{newUserUpdateSkippingSelf(auth.UserID(), event)})
}
