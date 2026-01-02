package handlers

import (
	"context"

	protocolwire "github.com/bhandras/delight/shared/wire"
)

// UsageReport validates and forwards usage report events as ephemerals.
func UsageReport(ctx context.Context, deps Deps, auth AuthContext, req protocolwire.UsageReportPayload) EventResult {
	if req.SessionID == "" || req.Key == "" {
		return NewEventResult(nil, nil)
	}

	session, err := deps.Sessions().GetSessionByID(ctx, req.SessionID)
	if err != nil || session.AccountID != auth.UserID() {
		return NewEventResult(nil, nil)
	}

	return NewEventResultWithEphemerals(nil, nil, []EphemeralInstruction{
		newEphemeralToUser(auth.UserID(), protocolwire.EphemeralUsagePayload{
			Type:      "usage",
			ID:        req.SessionID,
			Key:       req.Key,
			Tokens:    req.Tokens,
			Cost:      req.Cost,
			Timestamp: deps.Now().UnixMilli(),
		}),
	})
}
