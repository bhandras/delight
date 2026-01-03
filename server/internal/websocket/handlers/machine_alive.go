package handlers

import (
	"context"
	"time"

	"github.com/bhandras/delight/server/internal/models"
	protocolwire "github.com/bhandras/delight/shared/wire"
)

// TerminalAlive records a terminal keep-alive and emits a terminal-activity
// ephemeral to user-scoped sockets.
func TerminalAlive(ctx context.Context, deps Deps, auth AuthContext, req protocolwire.TerminalAlivePayload) EventResult {
	if req.TerminalID == "" {
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

	terminal, err := deps.Terminals().GetTerminal(ctx, models.GetTerminalParams{
		AccountID: auth.UserID(),
		ID:        req.TerminalID,
	})
	if err != nil || terminal.AccountID != auth.UserID() {
		return NewEventResult(nil, nil)
	}

	_ = deps.Terminals().UpdateTerminalActivity(ctx, models.UpdateTerminalActivityParams{
		Active:       1,
		LastActiveAt: time.UnixMilli(t),
		AccountID:    auth.UserID(),
		ID:           req.TerminalID,
	})

	return NewEventResultWithEphemerals(nil, nil, []EphemeralInstruction{
		newEphemeralToUserScoped(auth.UserID(), protocolwire.EphemeralTerminalActivityPayload{
			Type:     "terminal-activity",
			ID:       req.TerminalID,
			Active:   true,
			ActiveAt: t,
		}),
	})
}
