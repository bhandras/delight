package handlers

import (
	"context"
	"testing"
	"time"

	protocolwire "github.com/bhandras/delight/protocol/wire"
	"github.com/bhandras/delight/server/internal/models"
	"github.com/stretchr/testify/require"
)

type sessionAliveQueries struct {
	getByID        func(ctx context.Context, id string) (models.Session, error)
	updateActivity func(ctx context.Context, arg models.UpdateSessionActivityParams) error
}

func (s sessionAliveQueries) GetSessionByID(ctx context.Context, id string) (models.Session, error) {
	return s.getByID(ctx, id)
}

func (s sessionAliveQueries) UpdateSessionAgentState(ctx context.Context, arg models.UpdateSessionAgentStateParams) (int64, error) {
	return 0, nil
}

func (s sessionAliveQueries) UpdateSessionActivity(ctx context.Context, arg models.UpdateSessionActivityParams) error {
	return s.updateActivity(ctx, arg)
}

func (s sessionAliveQueries) UpdateSessionMetadata(ctx context.Context, arg models.UpdateSessionMetadataParams) (int64, error) {
	return 0, nil
}

func TestSessionAlive_IgnoresOldPings(t *testing.T) {
	sessions := sessionAliveQueries{
		getByID: func(ctx context.Context, id string) (models.Session, error) {
			return models.Session{ID: id, AccountID: "u1"}, nil
		},
		updateActivity: func(ctx context.Context, arg models.UpdateSessionActivityParams) error {
			t.Fatalf("unexpected update activity call")
			return nil
		},
	}
	now := time.UnixMilli(1000000)
	deps := NewDeps(nil, sessions, nil, nil, nil, func() time.Time { return now }, func() string { return "id" })

	res := SessionAlive(context.Background(), deps, NewAuthContext("u1", "session-scoped", "sock1"), protocolwire.SessionAlivePayload{
		SID:  "s1",
		Time: now.Add(-11 * time.Minute).UnixMilli(),
	})

	require.Empty(t, res.Ephemerals())
}

func TestSessionAlive_EmitsEphemeral(t *testing.T) {
	var got models.UpdateSessionActivityParams
	sessions := sessionAliveQueries{
		getByID: func(ctx context.Context, id string) (models.Session, error) {
			return models.Session{ID: id, AccountID: "u1"}, nil
		},
		updateActivity: func(ctx context.Context, arg models.UpdateSessionActivityParams) error {
			got = arg
			return nil
		},
	}
	now := time.UnixMilli(2000000)
	deps := NewDeps(nil, sessions, nil, nil, nil, func() time.Time { return now }, func() string { return "id" })

	res := SessionAlive(context.Background(), deps, NewAuthContext("u1", "session-scoped", "sock1"), protocolwire.SessionAlivePayload{
		SID:      "s1",
		Time:     now.UnixMilli(),
		Thinking: true,
	})

	require.Equal(t, int64(1), got.Active)
	require.Equal(t, "s1", got.ID)
	require.Len(t, res.Ephemerals(), 1)
	eph := res.Ephemerals()[0]
	require.True(t, eph.IsUser())
	payload, ok := eph.Payload().(protocolwire.EphemeralActivityPayload)
	require.True(t, ok)
	require.Equal(t, "activity", payload.Type)
	require.Equal(t, "s1", payload.ID)
	require.True(t, payload.Active)
	require.True(t, payload.Thinking)
}
