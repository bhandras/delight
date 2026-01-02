package handlers

import (
	"context"

	"github.com/bhandras/delight/protocol/logger"
	protocolwire "github.com/bhandras/delight/protocol/wire"
	"github.com/bhandras/delight/server/internal/models"
)

// ConnectMachineScoped marks a machine-scoped socket as active and emits a
// machine-activity ephemeral to user-scoped sockets.
func ConnectMachineScoped(ctx context.Context, deps Deps, auth AuthContext, machineID string) EventResult {
	if machineID == "" {
		return NewEventResult(nil, nil)
	}

	now := deps.Now()
	if err := deps.Machines().UpdateMachineActivity(ctx, models.UpdateMachineActivityParams{
		Active:       1,
		LastActiveAt: now,
		AccountID:    auth.UserID(),
		ID:           machineID,
	}); err != nil {
		logger.Warnf("Failed to update machine activity: %v", err)
	}

	return NewEventResultWithEphemerals(nil, nil, []EphemeralInstruction{
		newEphemeralToUserScoped(auth.UserID(), protocolwire.EphemeralMachineActivityPayload{
			Type:     "machine-activity",
			ID:       machineID,
			Active:   true,
			ActiveAt: now.UnixMilli(),
		}),
	})
}
