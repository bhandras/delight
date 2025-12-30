package handlers

import (
	"context"
	"database/sql"

	protocolwire "github.com/bhandras/delight/protocol/wire"
	"github.com/bhandras/delight/server/internal/models"
)

// AccessKeyGet returns the access key for a session+machine pair.
func AccessKeyGet(ctx context.Context, deps Deps, auth AuthContext, req protocolwire.AccessKeyGetRequest) EventResult {
	if req.SessionID == "" || req.MachineID == "" {
		return NewEventResult(protocolwire.AccessKeyLookupAck{
			OK:    false,
			Error: "Invalid parameters: sessionId and machineId are required",
		}, nil)
	}

	session, err := deps.Sessions().GetSessionByID(ctx, req.SessionID)
	if err != nil || session.AccountID != auth.UserID() {
		return NewEventResult(protocolwire.AccessKeyLookupAck{
			OK:        false,
			Error:     "Session or machine not found",
			AccessKey: nil,
		}, nil)
	}

	machine, err := deps.Machines().GetMachine(ctx, models.GetMachineParams{
		AccountID: auth.UserID(),
		ID:        req.MachineID,
	})
	if err != nil || machine.AccountID != auth.UserID() {
		return NewEventResult(protocolwire.AccessKeyLookupAck{
			OK:        false,
			Error:     "Session or machine not found",
			AccessKey: nil,
		}, nil)
	}

	accessKey, err := deps.AccessKeys().GetAccessKey(ctx, models.GetAccessKeyParams{
		AccountID: auth.UserID(),
		MachineID: req.MachineID,
		SessionID: req.SessionID,
	})
	if err == sql.ErrNoRows {
		return NewEventResult(protocolwire.AccessKeyLookupAck{OK: true, AccessKey: nil}, nil)
	}
	if err != nil {
		return NewEventResult(protocolwire.AccessKeyLookupAck{OK: false, Error: "Internal error", AccessKey: nil}, nil)
	}

	return NewEventResult(protocolwire.AccessKeyLookupAck{
		OK: true,
		AccessKey: &protocolwire.AccessKeyInfo{
			Data:        accessKey.Data,
			DataVersion: accessKey.DataVersion,
			CreatedAt:   accessKey.CreatedAt.UnixMilli(),
			UpdatedAt:   accessKey.UpdatedAt.UnixMilli(),
		},
	}, nil)
}
