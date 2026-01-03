package handlers

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/bhandras/delight/server/internal/models"
	protocolwire "github.com/bhandras/delight/shared/wire"
	"github.com/stretchr/testify/require"
)

func TestTerminalUpdateState_VersionMismatch(t *testing.T) {
	terminals := fakeTerminalQueries{
		getTerminal: func(ctx context.Context, arg models.GetTerminalParams) (models.Terminal, error) {
			return models.Terminal{
				ID:                 arg.ID,
				AccountID:          arg.AccountID,
				DaemonStateVersion: 5,
				DaemonState:        sql.NullString{Valid: true, String: "cur"},
			}, nil
		},
		updateTerminalMeta: func(ctx context.Context, arg models.UpdateTerminalMetadataParams) (int64, error) {
			return 0, nil
		},
		updateTerminalState: func(ctx context.Context, arg models.UpdateTerminalDaemonStateParams) (int64, error) {
			t.Fatalf("unexpected update call")
			return 0, nil
		},
	}
	deps := NewDeps(nil, nil, terminals, nil, time.Now, func() string { return "id" })

	res := TerminalUpdateState(context.Background(), deps, NewAuthContext("u1", "user-scoped", "sock1"), protocolwire.TerminalUpdateStatePayload{
		TerminalID:      "t1",
		DaemonState:     "new",
		ExpectedVersion: 4,
	})

	ack, ok := res.Ack().(protocolwire.VersionedAck)
	require.True(t, ok)
	require.Equal(t, "version-mismatch", ack.Result)
	require.Equal(t, int64(5), ack.Version)
	require.Equal(t, "cur", ack.DaemonState)
	require.Empty(t, res.Updates())
}

func TestTerminalUpdateState_Success(t *testing.T) {
	terminals := fakeTerminalQueries{
		getTerminal: func(ctx context.Context, arg models.GetTerminalParams) (models.Terminal, error) {
			return models.Terminal{
				ID:                 arg.ID,
				AccountID:          arg.AccountID,
				DaemonStateVersion: 2,
			}, nil
		},
		updateTerminalMeta: func(ctx context.Context, arg models.UpdateTerminalMetadataParams) (int64, error) {
			return 0, nil
		},
		updateTerminalState: func(ctx context.Context, arg models.UpdateTerminalDaemonStateParams) (int64, error) {
			require.Equal(t, "t1", arg.ID)
			require.Equal(t, "u1", arg.AccountID)
			require.Equal(t, int64(3), arg.DaemonStateVersion)
			require.Equal(t, int64(2), arg.DaemonStateVersion_2)
			require.True(t, arg.DaemonState.Valid)
			require.Equal(t, "daemon", arg.DaemonState.String)
			return 1, nil
		},
	}
	accounts := fakeAccountQueries{
		updateSeq: func(ctx context.Context, id string) (int64, error) {
			return 11, nil
		},
	}
	deps := NewDeps(accounts, nil, terminals, nil, func() time.Time { return time.UnixMilli(5555) }, func() string { return "evt1" })

	res := TerminalUpdateState(context.Background(), deps, NewAuthContext("u1", "user-scoped", "sock1"), protocolwire.TerminalUpdateStatePayload{
		TerminalID:      "t1",
		DaemonState:     "daemon",
		ExpectedVersion: 2,
	})

	ack, ok := res.Ack().(protocolwire.VersionedAck)
	require.True(t, ok)
	require.Equal(t, "success", ack.Result)
	require.Equal(t, int64(3), ack.Version)
	require.Equal(t, "daemon", ack.DaemonState)

	require.Len(t, res.Updates(), 1)
	upd := res.Updates()[0]
	require.True(t, upd.IsUser())
	require.True(t, upd.SkipSelf())
}
