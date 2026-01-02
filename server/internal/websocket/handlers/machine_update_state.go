package handlers

import (
	"context"
	"database/sql"

	"github.com/bhandras/delight/server/internal/models"
	"github.com/bhandras/delight/shared/logger"
	protocolwire "github.com/bhandras/delight/shared/wire"
)

// MachineUpdateState applies a machine daemon-state update and returns an ACK
// payload plus any resulting update events that should be broadcast.
func MachineUpdateState(ctx context.Context, deps Deps, auth AuthContext, req protocolwire.MachineUpdateStatePayload) EventResult {
	if req.MachineID == "" || req.DaemonState == "" {
		return NewEventResult(protocolwire.ResultAck{Result: "error", Message: "Invalid parameters"}, nil)
	}

	machine, err := deps.Machines().GetMachine(ctx, models.GetMachineParams{
		AccountID: auth.UserID(),
		ID:        req.MachineID,
	})
	if err != nil || machine.AccountID != auth.UserID() {
		return NewEventResult(protocolwire.ResultAck{Result: "error", Message: "Machine not found"}, nil)
	}

	if machine.DaemonStateVersion != req.ExpectedVersion {
		resp := protocolwire.VersionedAck{
			Result:  "version-mismatch",
			Version: machine.DaemonStateVersion,
		}
		if machine.DaemonState.Valid {
			resp.DaemonState = machine.DaemonState.String
		}
		return NewEventResult(resp, nil)
	}

	stateVal := sql.NullString{Valid: true, String: req.DaemonState}
	rows, err := deps.Machines().UpdateMachineDaemonState(ctx, models.UpdateMachineDaemonStateParams{
		DaemonState:          stateVal,
		DaemonStateVersion:   req.ExpectedVersion + 1,
		AccountID:            auth.UserID(),
		ID:                   req.MachineID,
		DaemonStateVersion_2: req.ExpectedVersion,
	})
	if err != nil || rows == 0 {
		current, err := deps.Machines().GetMachine(ctx, models.GetMachineParams{
			AccountID: auth.UserID(),
			ID:        req.MachineID,
		})
		if err == nil {
			resp := protocolwire.VersionedAck{
				Result:  "version-mismatch",
				Version: current.DaemonStateVersion,
			}
			if current.DaemonState.Valid {
				resp.DaemonState = current.DaemonState.String
			}
			return NewEventResult(resp, nil)
		}
		return NewEventResult(protocolwire.ResultAck{Result: "error"}, nil)
	}

	ack := protocolwire.VersionedAck{
		Result:      "success",
		Version:     req.ExpectedVersion + 1,
		DaemonState: req.DaemonState,
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
			DaemonState: &protocolwire.VersionedString{
				Value:   req.DaemonState,
				Version: req.ExpectedVersion + 1,
			},
		},
	}

	return NewEventResult(ack, []UpdateInstruction{newUserUpdateSkippingSelf(auth.UserID(), event)})
}
