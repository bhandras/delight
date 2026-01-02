package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/bhandras/delight/server/internal/models"
	protocolwire "github.com/bhandras/delight/shared/wire"
	"github.com/stretchr/testify/require"
)

func TestDisconnectEffects_SessionScoped(t *testing.T) {
	var updated models.UpdateSessionActivityParams
	sessions := fakeSessionQueries{
		getByID: func(ctx context.Context, id string) (models.Session, error) {
			return models.Session{ID: id, AccountID: "u1"}, nil
		},
		updateAgentState: func(ctx context.Context, arg models.UpdateSessionAgentStateParams) (int64, error) {
			return 0, nil
		},
		updateActivity: func(ctx context.Context, arg models.UpdateSessionActivityParams) error {
			updated = arg
			return nil
		},
		updateMetadata: func(ctx context.Context, arg models.UpdateSessionMetadataParams) (int64, error) {
			return 0, nil
		},
	}
	now := time.UnixMilli(5000000)
	deps := NewDeps(nil, sessions, nil, nil, nil, func() time.Time { return now }, func() string { return "id" })

	res := DisconnectEffects(context.Background(), deps, NewAuthContext("u1", "session-scoped", "sock1"), "s1", "")

	require.Equal(t, int64(0), updated.Active)
	require.Equal(t, "s1", updated.ID)
	require.Len(t, res.Ephemerals(), 1)
	payload, ok := res.Ephemerals()[0].Payload().(protocolwire.EphemeralActivityPayload)
	require.True(t, ok)
	require.False(t, payload.Active)
	require.Equal(t, "s1", payload.ID)
}
