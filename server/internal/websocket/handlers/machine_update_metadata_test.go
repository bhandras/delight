package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/bhandras/delight/server/internal/models"
	protocolwire "github.com/bhandras/delight/shared/wire"
	"github.com/stretchr/testify/require"
)

func TestMachineUpdateMetadata_InvalidParams(t *testing.T) {
	deps := NewDeps(nil, nil, nil, nil, nil, time.Now, func() string { return "id" })
	res := MachineUpdateMetadata(context.Background(), deps, NewAuthContext("u1", "user-scoped", "s1"), protocolwire.MachineUpdateMetadataPayload{})

	ack, ok := res.Ack().(protocolwire.ResultAck)
	require.True(t, ok)
	require.Equal(t, "error", ack.Result)
	require.Empty(t, res.Updates())
}

func TestMachineUpdateMetadata_VersionMismatch(t *testing.T) {
	machines := fakeMachineQueries{
		getMachine: func(ctx context.Context, arg models.GetMachineParams) (models.Machine, error) {
			return models.Machine{ID: arg.ID, AccountID: arg.AccountID, Metadata: "cur", MetadataVersion: 3}, nil
		},
		updateMachineMeta: func(ctx context.Context, arg models.UpdateMachineMetadataParams) (int64, error) {
			t.Fatalf("unexpected update call")
			return 0, nil
		},
		updateMachineState: func(ctx context.Context, arg models.UpdateMachineDaemonStateParams) (int64, error) {
			return 0, nil
		},
	}
	deps := NewDeps(nil, nil, machines, nil, nil, time.Now, func() string { return "id" })

	res := MachineUpdateMetadata(context.Background(), deps, NewAuthContext("u1", "user-scoped", "sock1"), protocolwire.MachineUpdateMetadataPayload{
		MachineID:       "m1",
		Metadata:        "new",
		ExpectedVersion: 2,
	})

	ack, ok := res.Ack().(protocolwire.VersionedAck)
	require.True(t, ok)
	require.Equal(t, "version-mismatch", ack.Result)
	require.Equal(t, int64(3), ack.Version)
	require.Equal(t, "cur", ack.Metadata)
	require.Empty(t, res.Updates())
}

func TestMachineUpdateMetadata_Success(t *testing.T) {
	machines := fakeMachineQueries{
		getMachine: func(ctx context.Context, arg models.GetMachineParams) (models.Machine, error) {
			return models.Machine{ID: arg.ID, AccountID: arg.AccountID, MetadataVersion: 7}, nil
		},
		updateMachineMeta: func(ctx context.Context, arg models.UpdateMachineMetadataParams) (int64, error) {
			require.Equal(t, "m1", arg.ID)
			require.Equal(t, "u1", arg.AccountID)
			require.Equal(t, "meta", arg.Metadata)
			require.Equal(t, int64(8), arg.MetadataVersion)
			require.Equal(t, int64(7), arg.MetadataVersion_2)
			return 1, nil
		},
		updateMachineState: func(ctx context.Context, arg models.UpdateMachineDaemonStateParams) (int64, error) {
			return 0, nil
		},
	}
	accounts := fakeAccountQueries{
		updateSeq: func(ctx context.Context, id string) (int64, error) {
			return 2, nil
		},
	}
	now := time.UnixMilli(4444)
	deps := NewDeps(accounts, nil, machines, nil, nil, func() time.Time { return now }, func() string { return "evt1" })

	res := MachineUpdateMetadata(context.Background(), deps, NewAuthContext("u1", "user-scoped", "sock1"), protocolwire.MachineUpdateMetadataPayload{
		MachineID:       "m1",
		Metadata:        "meta",
		ExpectedVersion: 7,
	})

	ack, ok := res.Ack().(protocolwire.VersionedAck)
	require.True(t, ok)
	require.Equal(t, "success", ack.Result)
	require.Equal(t, int64(8), ack.Version)
	require.Equal(t, "meta", ack.Metadata)

	require.Len(t, res.Updates(), 1)
	upd := res.Updates()[0]
	require.True(t, upd.IsUser())
	require.True(t, upd.SkipSelf())
}
