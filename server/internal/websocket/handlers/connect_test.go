package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/bhandras/delight/server/internal/models"
	protocolwire "github.com/bhandras/delight/shared/wire"
	"github.com/stretchr/testify/require"
)

func TestConnectTerminalScoped_EmitsUserScopedEphemeral(t *testing.T) {
	var updated models.UpdateTerminalActivityParams
	terminals := fakeTerminalQueries{
		getTerminal: func(ctx context.Context, arg models.GetTerminalParams) (models.Terminal, error) {
			return models.Terminal{}, nil
		},
		updateTerminalMeta: func(ctx context.Context, arg models.UpdateTerminalMetadataParams) (int64, error) {
			return 0, nil
		},
		updateActivity: func(ctx context.Context, arg models.UpdateTerminalActivityParams) error {
			updated = arg
			return nil
		},
		updateTerminalState: func(ctx context.Context, arg models.UpdateTerminalDaemonStateParams) (int64, error) {
			return 0, nil
		},
	}
	now := time.UnixMilli(12345)
	deps := NewDeps(nil, nil, terminals, nil, func() time.Time { return now }, func() string { return "id" })

	res := ConnectTerminalScoped(context.Background(), deps, NewAuthContext("u1", "terminal-scoped", "sock1"), "t1")

	require.Equal(t, int64(1), updated.Active)
	require.Equal(t, "t1", updated.ID)
	require.Equal(t, "u1", updated.AccountID)
	require.Len(t, res.Ephemerals(), 1)
	eph := res.Ephemerals()[0]
	require.True(t, eph.IsUserScopedOnly())
	payload, ok := eph.Payload().(protocolwire.EphemeralTerminalActivityPayload)
	require.True(t, ok)
	require.Equal(t, "t1", payload.ID)
	require.True(t, payload.Active)
}
