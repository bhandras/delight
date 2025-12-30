package handlers

import "context"

// EphemeralForward forwards arbitrary ephemeral payloads to user-scoped sockets.
func EphemeralForward(ctx context.Context, deps Deps, auth AuthContext, payload map[string]any) EventResult {
	if payload == nil {
		return NewEventResult(nil, nil)
	}
	return NewEventResultWithEphemerals(nil, nil, []EphemeralInstruction{
		newEphemeralToUserScoped(auth.UserID(), payload),
	})
}
