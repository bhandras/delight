package handlers

import (
	"context"
	"log"
	"time"

	protocolwire "github.com/bhandras/delight/protocol/wire"
	"github.com/bhandras/delight/server/internal/models"
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
		log.Printf("Failed to update session activity: %v", err)
	}

	return NewEventResultWithEphemerals(nil, nil, []EphemeralInstruction{
		newEphemeralToUser(auth.UserID(), protocolwire.EphemeralActivityPayload{
			Type:     "activity",
			ID:       req.SID,
			Active:   true,
			Thinking: req.Thinking,
			ActiveAt: t,
		}),
	})
}
