package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/bhandras/delight/server/internal/models"
	protocolwire "github.com/bhandras/delight/shared/wire"
	"github.com/stretchr/testify/require"
)

func TestUpdateMetadata_InvalidParams(t *testing.T) {
	deps := NewDeps(nil, nil, nil, nil, time.Now, func() string { return "id" })
	res := UpdateMetadata(context.Background(), deps, NewAuthContext("u1", "user-scoped", "s1"), protocolwire.UpdateMetadataPayload{})

	ack, ok := res.Ack().(protocolwire.ResultAck)
	require.True(t, ok)
	require.Equal(t, "error", ack.Result)
	require.Empty(t, res.Updates())
}

func TestUpdateMetadata_VersionMismatch(t *testing.T) {
	call := 0
	sessions := fakeSessionQueries{
		getByID: func(ctx context.Context, id string) (models.Session, error) {
			call++
			if call == 1 {
				return models.Session{ID: id, AccountID: "u1"}, nil
			}
			return models.Session{ID: id, AccountID: "u1", MetadataVersion: 5, Metadata: "cur"}, nil
		},
		updateAgentState: func(ctx context.Context, arg models.UpdateSessionAgentStateParams) (int64, error) {
			t.Fatalf("unexpected state update call")
			return 0, nil
		},
		updateMetadata: func(ctx context.Context, arg models.UpdateSessionMetadataParams) (int64, error) {
			return 0, nil
		},
	}
	deps := NewDeps(nil, sessions, nil, nil, time.Now, func() string { return "id" })

	res := UpdateMetadata(context.Background(), deps, NewAuthContext("u1", "user-scoped", "sock1"), protocolwire.UpdateMetadataPayload{
		SID:             "sess1",
		Metadata:        "new",
		ExpectedVersion: 4,
	})

	ack, ok := res.Ack().(protocolwire.VersionedAck)
	require.True(t, ok)
	require.Equal(t, "version-mismatch", ack.Result)
	require.Equal(t, int64(5), ack.Version)
	require.Equal(t, "cur", ack.Metadata)
	require.Empty(t, res.Updates())
}

func TestUpdateMetadata_Success(t *testing.T) {
	sessions := fakeSessionQueries{
		getByID: func(ctx context.Context, id string) (models.Session, error) {
			return models.Session{ID: id, AccountID: "u1"}, nil
		},
		updateAgentState: func(ctx context.Context, arg models.UpdateSessionAgentStateParams) (int64, error) {
			t.Fatalf("unexpected state update call")
			return 0, nil
		},
		updateMetadata: func(ctx context.Context, arg models.UpdateSessionMetadataParams) (int64, error) {
			require.Equal(t, "sess1", arg.ID)
			require.Equal(t, "meta", arg.Metadata)
			require.Equal(t, int64(2), arg.MetadataVersion)
			require.Equal(t, int64(1), arg.MetadataVersion_2)
			return 1, nil
		},
	}
	accounts := fakeAccountQueries{
		updateSeq: func(ctx context.Context, id string) (int64, error) {
			return 7, nil
		},
	}
	now := time.UnixMilli(3333)
	deps := NewDeps(accounts, sessions, nil, nil, func() time.Time { return now }, func() string { return "evt1" })

	res := UpdateMetadata(context.Background(), deps, NewAuthContext("u1", "user-scoped", "sock1"), protocolwire.UpdateMetadataPayload{
		SID:             "sess1",
		Metadata:        "meta",
		ExpectedVersion: 1,
	})

	ack, ok := res.Ack().(protocolwire.VersionedAck)
	require.True(t, ok)
	require.Equal(t, "success", ack.Result)
	require.Equal(t, int64(2), ack.Version)
	require.Equal(t, "meta", ack.Metadata)

	require.Len(t, res.Updates(), 1)
	upd := res.Updates()[0]
	require.True(t, upd.IsSession())
	require.True(t, upd.SkipSelf())

	ev := upd.Event()
	body, ok := ev.Body.(protocolwire.UpdateBodyUpdateSession)
	require.True(t, ok)
	require.NotNil(t, body.Metadata)
	require.Equal(t, "meta", body.Metadata.Value)
	require.Equal(t, int64(2), body.Metadata.Version)
}
