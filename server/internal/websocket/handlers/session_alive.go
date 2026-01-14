package handlers

import (
	"context"
	"time"

	"github.com/bhandras/delight/server/internal/models"
	"github.com/bhandras/delight/shared/logger"
	protocolwire "github.com/bhandras/delight/shared/wire"
)

// SessionAlive records a session keep-alive and emits an activity ephemeral.
func SessionAlive(ctx context.Context, deps Deps, auth AuthContext, req protocolwire.SessionAlivePayload) EventResult {
	if req.SID == "" {
		return NewEventResult(nil, nil)
	}

	t := req.Time
	if t == 0 {
		t = deps.Now().UnixMilli()
	}

	now := deps.Now().UnixMilli()
	if t > now {
		t = now
	}
	if t < now-10*60*1000 {
		return NewEventResult(nil, nil)
	}

	if err := deps.Sessions().UpdateSessionActivity(ctx, models.UpdateSessionActivityParams{
		Active:       1,
		LastActiveAt: time.UnixMilli(t),
		ID:           req.SID,
	}); err != nil {
		logger.Warnf("Failed to update session activity: %v", err)
	}

	// Persist turn boundaries so reconnecting clients can recover accurate busy
	// state even if they missed ephemeral events while backgrounded.
	if req.Working {
		if err := deps.Sessions().EnsureSessionTurnOpen(ctx, req.SID, t); err != nil {
			logger.Warnf("Failed to persist session turn start: %v", err)
		}
	} else {
		if err := deps.Sessions().EnsureSessionTurnClosed(ctx, req.SID, t); err != nil {
			logger.Warnf("Failed to persist session turn end: %v", err)
		}
	}

	return NewEventResultWithEphemerals(nil, nil, []EphemeralInstruction{
		newEphemeralToUser(auth.UserID(), protocolwire.EphemeralActivityPayload{
			Type:     "activity",
			ID:       req.SID,
			Active:   true,
			Working:  req.Working,
			ActiveAt: t,
		}),
	})
}
