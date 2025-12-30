package handlers

import (
	"context"
	"testing"
	"time"

	protocolwire "github.com/bhandras/delight/protocol/wire"
	"github.com/bhandras/delight/server/internal/models"
	"github.com/stretchr/testify/require"
)

func TestConnectMachineScoped_EmitsUserScopedEphemeral(t *testing.T) {
	var updated models.UpdateMachineActivityParams
	machines := fakeMachineQueries{
		getMachine: func(ctx context.Context, arg models.GetMachineParams) (models.Machine, error) {
			return models.Machine{}, nil
		},
		updateMachineMeta: func(ctx context.Context, arg models.UpdateMachineMetadataParams) (int64, error) {
			return 0, nil
		},
		updateActivity: func(ctx context.Context, arg models.UpdateMachineActivityParams) error {
			updated = arg
			return nil
		},
		updateMachineState: func(ctx context.Context, arg models.UpdateMachineDaemonStateParams) (int64, error) {
			return 0, nil
		},
	}
	now := time.UnixMilli(12345)
	deps := NewDeps(nil, nil, machines, nil, nil, func() time.Time { return now }, func() string { return "id" })

	res := ConnectMachineScoped(context.Background(), deps, NewAuthContext("u1", "machine-scoped", "sock1"), "m1")

	require.Equal(t, int64(1), updated.Active)
	require.Equal(t, "m1", updated.ID)
	require.Equal(t, "u1", updated.AccountID)
	require.Len(t, res.Ephemerals(), 1)
	eph := res.Ephemerals()[0]
	require.True(t, eph.IsUserScopedOnly())
	payload, ok := eph.Payload().(protocolwire.EphemeralMachineActivityPayload)
	require.True(t, ok)
	require.Equal(t, "m1", payload.ID)
	require.True(t, payload.Active)
}
