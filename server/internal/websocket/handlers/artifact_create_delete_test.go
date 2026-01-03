package handlers

import (
	"context"
	"database/sql"
	"encoding/base64"
	"testing"
	"time"

	"github.com/bhandras/delight/server/internal/models"
	protocolwire "github.com/bhandras/delight/shared/wire"
	"github.com/stretchr/testify/require"
)

func TestArtifactCreate_SuccessEmitsUpdate(t *testing.T) {
	var created bool
	artifacts := fakeArtifactQueries{
		getByID: func(ctx context.Context, id string) (models.Artifact, error) {
			return models.Artifact{}, sql.ErrNoRows
		},
		create: func(ctx context.Context, arg models.CreateArtifactParams) error {
			created = true
			require.Equal(t, "a1", arg.ID)
			require.Equal(t, "u1", arg.AccountID)
			return nil
		},
		getByIDAndAccount: func(ctx context.Context, arg models.GetArtifactByIDAndAccountParams) (models.Artifact, error) {
			require.True(t, created)
			return models.Artifact{
				ID:                arg.ID,
				AccountID:         arg.AccountID,
				Header:            []byte("h"),
				HeaderVersion:     1,
				Body:              []byte("b"),
				BodyVersion:       1,
				DataEncryptionKey: []byte("k"),
				Seq:               0,
				CreatedAt:         time.UnixMilli(1),
				UpdatedAt:         time.UnixMilli(2),
			}, nil
		},
		update: func(ctx context.Context, arg models.UpdateArtifactParams) (int64, error) {
			return 0, nil
		},
		del: func(ctx context.Context, arg models.DeleteArtifactParams) error { return nil },
	}
	accounts := fakeAccountQueries{
		updateSeq: func(ctx context.Context, id string) (int64, error) {
			return 5, nil
		},
	}
	now := time.UnixMilli(6666)
	deps := NewDeps(accounts, nil, nil, artifacts, func() time.Time { return now }, func() string { return "evt1" })

	req := protocolwire.ArtifactCreateRequest{
		ID:                "a1",
		Header:            base64.StdEncoding.EncodeToString([]byte("h")),
		Body:              base64.StdEncoding.EncodeToString([]byte("b")),
		DataEncryptionKey: base64.StdEncoding.EncodeToString([]byte("k")),
	}
	res := ArtifactCreate(context.Background(), deps, NewAuthContext("u1", "user-scoped", "sock1"), req)

	ack, ok := res.Ack().(protocolwire.ArtifactAck)
	require.True(t, ok)
	require.Equal(t, "success", ack.Result)
	require.NotNil(t, ack.Artifact)
	require.Len(t, res.Updates(), 1)
	require.True(t, res.Updates()[0].IsUser())
	require.False(t, res.Updates()[0].SkipSelf())
}

func TestArtifactDelete_SuccessEmitsUpdate(t *testing.T) {
	artifacts := fakeArtifactQueries{
		getByIDAndAccount: func(ctx context.Context, arg models.GetArtifactByIDAndAccountParams) (models.Artifact, error) {
			return models.Artifact{ID: arg.ID, AccountID: arg.AccountID}, nil
		},
		del: func(ctx context.Context, arg models.DeleteArtifactParams) error {
			require.Equal(t, "a1", arg.ID)
			require.Equal(t, "u1", arg.AccountID)
			return nil
		},
		getByID: func(ctx context.Context, id string) (models.Artifact, error) {
			return models.Artifact{}, sql.ErrNoRows
		},
		create: func(ctx context.Context, arg models.CreateArtifactParams) error { return nil },
		update: func(ctx context.Context, arg models.UpdateArtifactParams) (int64, error) { return 0, nil },
	}
	accounts := fakeAccountQueries{
		updateSeq: func(ctx context.Context, id string) (int64, error) {
			return 9, nil
		},
	}
	now := time.UnixMilli(7777)
	deps := NewDeps(accounts, nil, nil, artifacts, func() time.Time { return now }, func() string { return "evt1" })

	res := ArtifactDelete(context.Background(), deps, NewAuthContext("u1", "user-scoped", "sock1"), protocolwire.ArtifactDeleteRequest{
		ArtifactID: "a1",
	})

	ack, ok := res.Ack().(protocolwire.ResultAck)
	require.True(t, ok)
	require.Equal(t, "success", ack.Result)
	require.Len(t, res.Updates(), 1)
	require.True(t, res.Updates()[0].IsUser())
	require.False(t, res.Updates()[0].SkipSelf())
}
