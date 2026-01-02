package handlers

import (
	"context"
	"encoding/json"

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

	tokens := structToMap(req.Tokens)
	cost := structToMap(req.Cost)
	if tokens == nil || cost == nil {
		return NewEventResult(nil, nil)
	}

	return NewEventResultWithEphemerals(nil, nil, []EphemeralInstruction{
		newEphemeralToUser(auth.UserID(), protocolwire.EphemeralUsagePayload{
			Type:      "usage",
			ID:        req.SessionID,
			Key:       req.Key,
			Tokens:    tokens,
			Cost:      cost,
			Timestamp: deps.Now().UnixMilli(),
		}),
	})
}

func structToMap[T any](v T) map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}
