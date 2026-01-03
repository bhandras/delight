package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/bhandras/delight/server/internal/models"
	protocolwire "github.com/bhandras/delight/shared/wire"
	"github.com/stretchr/testify/require"
)

type usageSessionQueries struct {
	get func(ctx context.Context, id string) (models.Session, error)
}

func (u usageSessionQueries) GetSessionByID(ctx context.Context, id string) (models.Session, error) {
	return u.get(ctx, id)
}

func (u usageSessionQueries) UpdateSessionAgentState(ctx context.Context, arg models.UpdateSessionAgentStateParams) (int64, error) {
	return 0, nil
}

func (u usageSessionQueries) UpdateSessionActivity(ctx context.Context, arg models.UpdateSessionActivityParams) error {
	return nil
}

func (u usageSessionQueries) UpdateSessionMetadata(ctx context.Context, arg models.UpdateSessionMetadataParams) (int64, error) {
	return 0, nil
}

func TestUsageReport_EmitsEphemeral(t *testing.T) {
	sessions := usageSessionQueries{
		get: func(ctx context.Context, id string) (models.Session, error) {
			return models.Session{ID: id, AccountID: "u1"}, nil
		},
	}
	now := time.UnixMilli(4000000)
	deps := NewDeps(nil, sessions, nil, nil, func() time.Time { return now }, func() string { return "id" })

	res := UsageReport(context.Background(), deps, NewAuthContext("u1", "session-scoped", "sock1"), protocolwire.UsageReportPayload{
		Key:       "k",
		SessionID: "s1",
		Tokens: protocolwire.UsageReportTokens{
			Total:  3,
			Input:  1,
			Output: 2,
		},
		Cost: protocolwire.UsageReportCost{
			Total: 0.01,
			Input: 0.002,
		},
	})

	require.Len(t, res.Ephemerals(), 1)
	eph := res.Ephemerals()[0]
	require.True(t, eph.IsUser())
	payload, ok := eph.Payload().(protocolwire.EphemeralUsagePayload)
	require.True(t, ok)
	require.Equal(t, "usage", payload.Type)
	require.Equal(t, "s1", payload.ID)
	require.Equal(t, "k", payload.Key)
	require.Equal(t, 3, payload.Tokens.Total)
	require.Equal(t, 1, payload.Tokens.Input)
	require.Equal(t, 2, payload.Tokens.Output)
	require.Equal(t, 0.01, payload.Cost.Total)
	require.Equal(t, 0.002, payload.Cost.Input)
}
