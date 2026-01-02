package handlers

import (
	"context"

	"github.com/bhandras/delight/server/internal/models"
	"github.com/bhandras/delight/shared/logger"
	protocolwire "github.com/bhandras/delight/shared/wire"
)

// DisconnectEffects applies disconnect-side effects for a socket based on its
// scope. It emits activity ephemerals and marks sessions/machines inactive.
func DisconnectEffects(ctx context.Context, deps Deps, auth AuthContext, sessionID, machineID string) EventResult {
	var ephemerals []EphemeralInstruction
	now := deps.Now()

	if auth.ClientType() == "session-scoped" && sessionID != "" {
		if err := deps.Sessions().UpdateSessionActivity(ctx, models.UpdateSessionActivityParams{
			Active:       0,
			LastActiveAt: now,
			ID:           sessionID,
		}); err != nil {
			logger.Warnf("Failed to update session activity: %v", err)
		}

		ephemerals = append(ephemerals, newEphemeralToUser(auth.UserID(), protocolwire.EphemeralActivityPayload{
			Type:     "activity",
			ID:       sessionID,
			Active:   false,
			Thinking: false,
			ActiveAt: now.UnixMilli(),
		}))
	}

	if auth.ClientType() == "machine-scoped" && machineID != "" {
		if err := deps.Machines().UpdateMachineActivity(ctx, models.UpdateMachineActivityParams{
			Active:       0,
			LastActiveAt: now,
			AccountID:    auth.UserID(),
			ID:           machineID,
		}); err != nil {
			logger.Warnf("Failed to update machine activity: %v", err)
		}

		ephemerals = append(ephemerals, newEphemeralToUserScoped(auth.UserID(), protocolwire.EphemeralMachineActivityPayload{
			Type:     "machine-activity",
			ID:       machineID,
			Active:   false,
			ActiveAt: now.UnixMilli(),
		}))
	}

	return NewEventResultWithEphemerals(nil, nil, ephemerals)
}
