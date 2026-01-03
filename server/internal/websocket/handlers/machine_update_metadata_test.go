package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/bhandras/delight/server/internal/models"
	protocolwire "github.com/bhandras/delight/shared/wire"
	"github.com/stretchr/testify/require"
)

func TestTerminalUpdateMetadata_InvalidParams(t *testing.T) {
	deps := NewDeps(nil, nil, nil, nil, time.Now, func() string { return "id" })
	res := TerminalUpdateMetadata(context.Background(), deps, NewAuthContext("u1", "user-scoped", "s1"), protocolwire.TerminalUpdateMetadataPayload{})

	ack, ok := res.Ack().(protocolwire.ResultAck)
	require.True(t, ok)
	require.Equal(t, "error", ack.Result)
	require.Empty(t, res.Updates())
}

func TestTerminalUpdateMetadata_VersionMismatch(t *testing.T) {
	terminals := fakeTerminalQueries{
		getTerminal: func(ctx context.Context, arg models.GetTerminalParams) (models.Terminal, error) {
			return models.Terminal{ID: arg.ID, AccountID: arg.AccountID, Metadata: "cur", MetadataVersion: 3}, nil
		},
		updateTerminalMeta: func(ctx context.Context, arg models.UpdateTerminalMetadataParams) (int64, error) {
			t.Fatalf("unexpected update call")
			return 0, nil
		},
		updateTerminalState: func(ctx context.Context, arg models.UpdateTerminalDaemonStateParams) (int64, error) {
			return 0, nil
		},
	}
	deps := NewDeps(nil, nil, terminals, nil, time.Now, func() string { return "id" })

	res := TerminalUpdateMetadata(context.Background(), deps, NewAuthContext("u1", "user-scoped", "sock1"), protocolwire.TerminalUpdateMetadataPayload{
		TerminalID:      "t1",
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

func TestTerminalUpdateMetadata_Success(t *testing.T) {
	terminals := fakeTerminalQueries{
		getTerminal: func(ctx context.Context, arg models.GetTerminalParams) (models.Terminal, error) {
			return models.Terminal{ID: arg.ID, AccountID: arg.AccountID, MetadataVersion: 7}, nil
		},
		updateTerminalMeta: func(ctx context.Context, arg models.UpdateTerminalMetadataParams) (int64, error) {
			require.Equal(t, "t1", arg.ID)
			require.Equal(t, "u1", arg.AccountID)
			require.Equal(t, "meta", arg.Metadata)
			require.Equal(t, int64(8), arg.MetadataVersion)
			require.Equal(t, int64(7), arg.MetadataVersion_2)
			return 1, nil
		},
		updateTerminalState: func(ctx context.Context, arg models.UpdateTerminalDaemonStateParams) (int64, error) {
			return 0, nil
		},
	}
	accounts := fakeAccountQueries{
		updateSeq: func(ctx context.Context, id string) (int64, error) {
			return 2, nil
		},
	}
	now := time.UnixMilli(4444)
	deps := NewDeps(accounts, nil, terminals, nil, func() time.Time { return now }, func() string { return "evt1" })

	res := TerminalUpdateMetadata(context.Background(), deps, NewAuthContext("u1", "user-scoped", "sock1"), protocolwire.TerminalUpdateMetadataPayload{
		TerminalID:      "t1",
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
