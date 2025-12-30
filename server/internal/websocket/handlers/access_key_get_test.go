package handlers

import (
	"context"
	"database/sql"
	"testing"
	"time"

	protocolwire "github.com/bhandras/delight/protocol/wire"
	"github.com/bhandras/delight/server/internal/models"
	"github.com/stretchr/testify/require"
)

func TestAccessKeyGet_InvalidParams(t *testing.T) {
	deps := NewDeps(nil, nil, nil, nil, nil, time.Now, func() string { return "id" })
	res := AccessKeyGet(context.Background(), deps, NewAuthContext("u1", "user-scoped", "sock1"), protocolwire.AccessKeyGetRequest{})

	ack, ok := res.Ack().(protocolwire.AccessKeyLookupAck)
	require.True(t, ok)
	require.False(t, ack.OK)
	require.NotEmpty(t, ack.Error)
}

func TestAccessKeyGet_NoRows(t *testing.T) {
	sessions := fakeSessionQueries{
		getByID: func(ctx context.Context, id string) (models.Session, error) {
			return models.Session{ID: id, AccountID: "u1"}, nil
		},
		updateAgentState: func(ctx context.Context, arg models.UpdateSessionAgentStateParams) (int64, error) {
			return 0, nil
		},
		updateMetadata: func(ctx context.Context, arg models.UpdateSessionMetadataParams) (int64, error) {
			return 0, nil
		},
	}
	machines := fakeMachineQueries{
		getMachine: func(ctx context.Context, arg models.GetMachineParams) (models.Machine, error) {
			return models.Machine{ID: arg.ID, AccountID: arg.AccountID}, nil
		},
		updateMachineMeta: func(ctx context.Context, arg models.UpdateMachineMetadataParams) (int64, error) {
			return 0, nil
		},
		updateMachineState: func(ctx context.Context, arg models.UpdateMachineDaemonStateParams) (int64, error) {
			return 0, nil
		},
	}
	accessKeys := fakeAccessKeyQueries{
		get: func(ctx context.Context, arg models.GetAccessKeyParams) (models.AccessKey, error) {
			return models.AccessKey{}, sql.ErrNoRows
		},
	}
	deps := NewDeps(nil, sessions, machines, accessKeys, nil, time.Now, func() string { return "id" })

	res := AccessKeyGet(context.Background(), deps, NewAuthContext("u1", "user-scoped", "sock1"), protocolwire.AccessKeyGetRequest{
		SessionID: "s1",
		MachineID: "m1",
	})

	ack, ok := res.Ack().(protocolwire.AccessKeyLookupAck)
	require.True(t, ok)
	require.True(t, ack.OK)
	require.Nil(t, ack.AccessKey)
}

func TestAccessKeyGet_Success(t *testing.T) {
	sessions := fakeSessionQueries{
		getByID: func(ctx context.Context, id string) (models.Session, error) {
			return models.Session{ID: id, AccountID: "u1"}, nil
		},
		updateAgentState: func(ctx context.Context, arg models.UpdateSessionAgentStateParams) (int64, error) {
			return 0, nil
		},
		updateMetadata: func(ctx context.Context, arg models.UpdateSessionMetadataParams) (int64, error) {
			return 0, nil
		},
	}
	machines := fakeMachineQueries{
		getMachine: func(ctx context.Context, arg models.GetMachineParams) (models.Machine, error) {
			return models.Machine{ID: arg.ID, AccountID: arg.AccountID}, nil
		},
		updateMachineMeta: func(ctx context.Context, arg models.UpdateMachineMetadataParams) (int64, error) {
			return 0, nil
		},
		updateMachineState: func(ctx context.Context, arg models.UpdateMachineDaemonStateParams) (int64, error) {
			return 0, nil
		},
	}
	accessKeys := fakeAccessKeyQueries{
		get: func(ctx context.Context, arg models.GetAccessKeyParams) (models.AccessKey, error) {
			return models.AccessKey{
				Data:        "enc",
				DataVersion: 9,
				CreatedAt:   time.UnixMilli(1),
				UpdatedAt:   time.UnixMilli(2),
			}, nil
		},
	}
	deps := NewDeps(nil, sessions, machines, accessKeys, nil, time.Now, func() string { return "id" })

	res := AccessKeyGet(context.Background(), deps, NewAuthContext("u1", "user-scoped", "sock1"), protocolwire.AccessKeyGetRequest{
		SessionID: "s1",
		MachineID: "m1",
	})

	ack, ok := res.Ack().(protocolwire.AccessKeyLookupAck)
	require.True(t, ok)
	require.True(t, ack.OK)
	require.NotNil(t, ack.AccessKey)
	require.Equal(t, "enc", ack.AccessKey.Data)
	require.Equal(t, int64(9), ack.AccessKey.DataVersion)
}
