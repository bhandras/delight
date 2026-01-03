package handlers

import (
	"context"

	"github.com/bhandras/delight/server/internal/models"
	"github.com/bhandras/delight/shared/logger"
	protocolwire "github.com/bhandras/delight/shared/wire"
)

// ConnectTerminalScoped marks a terminal-scoped socket as active and emits a
// terminal-activity ephemeral to user-scoped sockets.
func ConnectTerminalScoped(ctx context.Context, deps Deps, auth AuthContext, terminalID string) EventResult {
	if terminalID == "" {
		return NewEventResult(nil, nil)
	}

	now := deps.Now()
	if err := deps.Terminals().UpdateTerminalActivity(ctx, models.UpdateTerminalActivityParams{
		Active:       1,
		LastActiveAt: now,
		AccountID:    auth.UserID(),
		ID:           terminalID,
	}); err != nil {
		logger.Warnf("Failed to update terminal activity: %v", err)
	}

	return NewEventResultWithEphemerals(nil, nil, []EphemeralInstruction{
		newEphemeralToUserScoped(auth.UserID(), protocolwire.EphemeralTerminalActivityPayload{
			Type:     "terminal-activity",
			ID:       terminalID,
			Active:   true,
			ActiveAt: now.UnixMilli(),
		}),
	})
}
