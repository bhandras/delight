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

type fakeAccountQueries struct {
	updateSeq func(ctx context.Context, id string) (int64, error)
}

func (f fakeAccountQueries) UpdateAccountSeq(ctx context.Context, id string) (int64, error) {
	return f.updateSeq(ctx, id)
}

type fakeSessionQueries struct {
	getByID          func(ctx context.Context, id string) (models.Session, error)
	updateAgentState func(ctx context.Context, arg models.UpdateSessionAgentStateParams) (int64, error)
	updateActivity   func(ctx context.Context, arg models.UpdateSessionActivityParams) error
	updateMetadata   func(ctx context.Context, arg models.UpdateSessionMetadataParams) (int64, error)
}

func (f fakeSessionQueries) GetSessionByID(ctx context.Context, id string) (models.Session, error) {
	return f.getByID(ctx, id)
}

func (f fakeSessionQueries) UpdateSessionAgentState(ctx context.Context, arg models.UpdateSessionAgentStateParams) (int64, error) {
	return f.updateAgentState(ctx, arg)
}

func (f fakeSessionQueries) UpdateSessionActivity(ctx context.Context, arg models.UpdateSessionActivityParams) error {
	if f.updateActivity == nil {
		return nil
	}
	return f.updateActivity(ctx, arg)
}

func (f fakeSessionQueries) UpdateSessionMetadata(ctx context.Context, arg models.UpdateSessionMetadataParams) (int64, error) {
	return f.updateMetadata(ctx, arg)
}

func TestUpdateState_InvalidParams(t *testing.T) {
	deps := NewDeps(nil, nil, nil, nil, nil, time.Now, func() string { return "id" })
	res := UpdateState(context.Background(), deps, NewAuthContext("u1", "user-scoped", "s1"), protocolwire.UpdateStatePayload{})

	ack, ok := res.Ack().(protocolwire.ResultAck)
	require.True(t, ok)
	require.Equal(t, "error", ack.Result)
	require.Empty(t, res.Updates())
}

func TestUpdateState_Unauthorized(t *testing.T) {
	sessions := fakeSessionQueries{
		getByID: func(ctx context.Context, id string) (models.Session, error) {
			return models.Session{ID: id, AccountID: "other"}, nil
		},
		updateAgentState: func(ctx context.Context, arg models.UpdateSessionAgentStateParams) (int64, error) {
			t.Fatalf("unexpected update call")
			return 0, nil
		},
		updateMetadata: func(ctx context.Context, arg models.UpdateSessionMetadataParams) (int64, error) {
			t.Fatalf("unexpected update call")
			return 0, nil
		},
	}
	deps := NewDeps(nil, sessions, nil, nil, nil, time.Now, func() string { return "id" })

	res := UpdateState(context.Background(), deps, NewAuthContext("u1", "user-scoped", "s1"), protocolwire.UpdateStatePayload{
		SID: "sess1",
	})

	ack, ok := res.Ack().(protocolwire.ResultAck)
	require.True(t, ok)
	require.Equal(t, "error", ack.Result)
	require.Empty(t, res.Updates())
}

func TestUpdateState_VersionMismatch(t *testing.T) {
	call := 0
	sessions := fakeSessionQueries{
		getByID: func(ctx context.Context, id string) (models.Session, error) {
			call++
			if call == 1 {
				return models.Session{ID: id, AccountID: "u1"}, nil
			}
			return models.Session{
				ID:                id,
				AccountID:         "u1",
				AgentStateVersion: 7,
				AgentState:        sql.NullString{Valid: true, String: "cipher"},
			}, nil
		},
		updateAgentState: func(ctx context.Context, arg models.UpdateSessionAgentStateParams) (int64, error) {
			return 0, nil
		},
		updateMetadata: func(ctx context.Context, arg models.UpdateSessionMetadataParams) (int64, error) {
			t.Fatalf("unexpected metadata update call")
			return 0, nil
		},
	}
	deps := NewDeps(nil, sessions, nil, nil, nil, time.Now, func() string { return "id" })

	state := "new"
	res := UpdateState(context.Background(), deps, NewAuthContext("u1", "user-scoped", "s1"), protocolwire.UpdateStatePayload{
		SID:             "sess1",
		AgentState:      &state,
		ExpectedVersion: 6,
	})

	ack, ok := res.Ack().(protocolwire.VersionedAck)
	require.True(t, ok)
	require.Equal(t, "version-mismatch", ack.Result)
	require.Equal(t, int64(7), ack.Version)
	require.Equal(t, "cipher", ack.AgentState)
	require.Empty(t, res.Updates())
}

func TestUpdateState_Success(t *testing.T) {
	sessions := fakeSessionQueries{
		getByID: func(ctx context.Context, id string) (models.Session, error) {
			return models.Session{ID: id, AccountID: "u1"}, nil
		},
		updateAgentState: func(ctx context.Context, arg models.UpdateSessionAgentStateParams) (int64, error) {
			require.Equal(t, "sess1", arg.ID)
			require.Equal(t, int64(10), arg.AgentStateVersion)
			require.Equal(t, int64(9), arg.AgentStateVersion_2)
			require.True(t, arg.AgentState.Valid)
			require.Equal(t, "cipher", arg.AgentState.String)
			return 1, nil
		},
		updateMetadata: func(ctx context.Context, arg models.UpdateSessionMetadataParams) (int64, error) {
			t.Fatalf("unexpected metadata update call")
			return 0, nil
		},
	}
	accounts := fakeAccountQueries{
		updateSeq: func(ctx context.Context, id string) (int64, error) {
			require.Equal(t, "u1", id)
			return 42, nil
		},
	}
	now := time.UnixMilli(1234)
	deps := NewDeps(accounts, sessions, nil, nil, nil, func() time.Time { return now }, func() string { return "evt1" })

	state := "cipher"
	res := UpdateState(context.Background(), deps, NewAuthContext("u1", "user-scoped", "sock1"), protocolwire.UpdateStatePayload{
		SID:             "sess1",
		AgentState:      &state,
		ExpectedVersion: 9,
	})

	ack, ok := res.Ack().(protocolwire.VersionedAck)
	require.True(t, ok)
	require.Equal(t, "success", ack.Result)
	require.Equal(t, int64(10), ack.Version)
	require.Equal(t, "cipher", ack.AgentState)

	require.Len(t, res.Updates(), 1)
	upd := res.Updates()[0]
	require.True(t, upd.IsSession())
	require.Equal(t, "u1", upd.UserID())
	require.Equal(t, "sess1", upd.SessionID())

	ev := upd.Event()
	require.Equal(t, "evt1", ev.ID)
	require.Equal(t, int64(42), ev.Seq)
	require.Equal(t, now.UnixMilli(), ev.CreatedAt)

	body, ok := ev.Body.(protocolwire.UpdateBodyUpdateSession)
	require.True(t, ok)
	require.Equal(t, "update-session", body.T)
	require.Equal(t, "sess1", body.ID)
	require.NotNil(t, body.AgentState)
	require.Equal(t, "cipher", body.AgentState.Value)
	require.Equal(t, int64(10), body.AgentState.Version)
}
