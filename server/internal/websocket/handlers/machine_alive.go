package handlers

import (
	"context"
	"time"

	protocolwire "github.com/bhandras/delight/protocol/wire"
	"github.com/bhandras/delight/server/internal/models"
)

// MachineAlive records a machine keep-alive and emits a machine-activity
// ephemeral to user-scoped sockets.
func MachineAlive(ctx context.Context, deps Deps, auth AuthContext, req protocolwire.MachineAlivePayload) EventResult {
	if req.MachineID == "" {
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

	machine, err := deps.Machines().GetMachine(ctx, models.GetMachineParams{
		AccountID: auth.UserID(),
		ID:        req.MachineID,
	})
	if err != nil || machine.AccountID != auth.UserID() {
		return NewEventResult(nil, nil)
	}

	_ = deps.Machines().UpdateMachineActivity(ctx, models.UpdateMachineActivityParams{
		Active:       1,
		LastActiveAt: time.UnixMilli(t),
		AccountID:    auth.UserID(),
		ID:           req.MachineID,
	})

	return NewEventResultWithEphemerals(nil, nil, []EphemeralInstruction{
		newEphemeralToUserScoped(auth.UserID(), protocolwire.EphemeralMachineActivityPayload{
			Type:     "machine-activity",
			ID:       req.MachineID,
			Active:   true,
			ActiveAt: t,
		}),
	})
}
