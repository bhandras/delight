package handlers

import (
	"context"
	"database/sql"

	"github.com/bhandras/delight/server/internal/models"
	"github.com/bhandras/delight/shared/logger"
	protocolwire "github.com/bhandras/delight/shared/wire"
)

// TerminalUpdateState applies a terminal daemon-state update and returns an ACK
// payload plus any resulting update events that should be broadcast.
func TerminalUpdateState(ctx context.Context, deps Deps, auth AuthContext, req protocolwire.TerminalUpdateStatePayload) EventResult {
	if req.TerminalID == "" || req.DaemonState == "" {
		return NewEventResult(protocolwire.ResultAck{Result: "error", Message: "Invalid parameters"}, nil)
	}

	terminal, err := deps.Terminals().GetTerminal(ctx, models.GetTerminalParams{
		AccountID: auth.UserID(),
		ID:        req.TerminalID,
	})
	if err != nil || terminal.AccountID != auth.UserID() {
		return NewEventResult(protocolwire.ResultAck{Result: "error", Message: "Terminal not found"}, nil)
	}

	if terminal.DaemonStateVersion != req.ExpectedVersion {
		resp := protocolwire.VersionedAck{
			Result:  "version-mismatch",
			Version: terminal.DaemonStateVersion,
		}
		if terminal.DaemonState.Valid {
			resp.DaemonState = terminal.DaemonState.String
		}
		return NewEventResult(resp, nil)
	}

	stateVal := sql.NullString{Valid: true, String: req.DaemonState}
	rows, err := deps.Terminals().UpdateTerminalDaemonState(ctx, models.UpdateTerminalDaemonStateParams{
		DaemonState:          stateVal,
		DaemonStateVersion:   req.ExpectedVersion + 1,
		AccountID:            auth.UserID(),
		ID:                   req.TerminalID,
		DaemonStateVersion_2: req.ExpectedVersion,
	})
	if err != nil || rows == 0 {
		current, err := deps.Terminals().GetTerminal(ctx, models.GetTerminalParams{
			AccountID: auth.UserID(),
			ID:        req.TerminalID,
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
		Body: protocolwire.UpdateBodyUpdateTerminal{
			T:          "update-terminal",
			TerminalID: req.TerminalID,
			DaemonState: &protocolwire.VersionedString{
				Value:   req.DaemonState,
				Version: req.ExpectedVersion + 1,
			},
		},
	}

	return NewEventResult(ack, []UpdateInstruction{newUserUpdateSkippingSelf(auth.UserID(), event)})
}
