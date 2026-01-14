package handlers

import (
	"context"

	"github.com/bhandras/delight/server/internal/models"
	"github.com/bhandras/delight/shared/logger"
	protocolwire "github.com/bhandras/delight/shared/wire"
)

// DisconnectEffects applies disconnect-side effects for a socket based on its
// scope. It emits activity ephemerals and marks sessions/terminals inactive.
func DisconnectEffects(ctx context.Context, deps Deps, auth AuthContext, sessionID, terminalID string) EventResult {
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

		// If the CLI goes offline mid-turn, force-close any in-flight turn so
		// reconnecting clients do not show stuck "thinking" state.
		if err := deps.Sessions().EnsureSessionTurnClosed(ctx, sessionID, now.UnixMilli()); err != nil {
			logger.Warnf("Failed to close session turn on disconnect: %v", err)
		}

		ephemerals = append(ephemerals, newEphemeralToUser(auth.UserID(), protocolwire.EphemeralActivityPayload{
			Type:     "activity",
			ID:       sessionID,
			Active:   false,
			Working:  false,
			ActiveAt: now.UnixMilli(),
		}))
	}

	if auth.ClientType() == "terminal-scoped" && terminalID != "" {
		if err := deps.Terminals().UpdateTerminalActivity(ctx, models.UpdateTerminalActivityParams{
			Active:       0,
			LastActiveAt: now,
			AccountID:    auth.UserID(),
			ID:           terminalID,
		}); err != nil {
			logger.Warnf("Failed to update terminal activity: %v", err)
		}

		ephemerals = append(ephemerals, newEphemeralToUserScoped(auth.UserID(), protocolwire.EphemeralTerminalActivityPayload{
			Type:     "terminal-activity",
			ID:       terminalID,
			Active:   false,
			ActiveAt: now.UnixMilli(),
		}))
	}

	return NewEventResultWithEphemerals(nil, nil, ephemerals)
}
