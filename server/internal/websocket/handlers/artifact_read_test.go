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

func TestArtifactRead_Success(t *testing.T) {
	artifacts := fakeArtifactQueries{
		getByIDAndAccount: func(ctx context.Context, arg models.GetArtifactByIDAndAccountParams) (models.Artifact, error) {
			return models.Artifact{
				ID:            arg.ID,
				AccountID:     arg.AccountID,
				Header:        []byte("h"),
				HeaderVersion: 1,
				Body:          []byte("b"),
				BodyVersion:   2,
				Seq:           3,
				CreatedAt:     time.UnixMilli(1),
				UpdatedAt:     time.UnixMilli(2),
			}, nil
		},
		update: func(ctx context.Context, arg models.UpdateArtifactParams) (int64, error) {
			return 0, nil
		},
		getByID: func(ctx context.Context, id string) (models.Artifact, error) {
			return models.Artifact{}, sql.ErrNoRows
		},
		create: func(ctx context.Context, arg models.CreateArtifactParams) error { return nil },
		del:    func(ctx context.Context, arg models.DeleteArtifactParams) error { return nil },
	}
	deps := NewDeps(nil, nil, nil, artifacts, time.Now, func() string { return "id" })

	res := ArtifactRead(context.Background(), deps, NewAuthContext("u1", "user-scoped", "sock1"), protocolwire.ArtifactReadRequest{
		ArtifactID: "a1",
	})

	ack, ok := res.Ack().(protocolwire.ArtifactAck)
	require.True(t, ok)
	require.Equal(t, "success", ack.Result)
	require.NotNil(t, ack.Artifact)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("h")), ack.Artifact.Header)
	require.Equal(t, int64(1), ack.Artifact.HeaderVersion)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("b")), ack.Artifact.Body)
	require.Equal(t, int64(2), ack.Artifact.BodyVersion)
}
