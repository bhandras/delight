package handlers

import (
	"context"
	"database/sql"
	"encoding/base64"
	"testing"
	"time"

	protocolwire "github.com/bhandras/delight/protocol/wire"
	"github.com/bhandras/delight/server/internal/models"
	"github.com/stretchr/testify/require"
)

type fakeArtifactQueries struct {
	getByIDAndAccount func(ctx context.Context, arg models.GetArtifactByIDAndAccountParams) (models.Artifact, error)
	update            func(ctx context.Context, arg models.UpdateArtifactParams) (int64, error)

	getByID func(ctx context.Context, id string) (models.Artifact, error)
	create  func(ctx context.Context, arg models.CreateArtifactParams) error
	del     func(ctx context.Context, arg models.DeleteArtifactParams) error
}

func (f fakeArtifactQueries) GetArtifactByID(ctx context.Context, id string) (models.Artifact, error) {
	return f.getByID(ctx, id)
}

func (f fakeArtifactQueries) GetArtifactByIDAndAccount(ctx context.Context, arg models.GetArtifactByIDAndAccountParams) (models.Artifact, error) {
	return f.getByIDAndAccount(ctx, arg)
}

func (f fakeArtifactQueries) CreateArtifact(ctx context.Context, arg models.CreateArtifactParams) error {
	return f.create(ctx, arg)
}

func (f fakeArtifactQueries) UpdateArtifact(ctx context.Context, arg models.UpdateArtifactParams) (int64, error) {
	return f.update(ctx, arg)
}

func (f fakeArtifactQueries) DeleteArtifact(ctx context.Context, arg models.DeleteArtifactParams) error {
	return f.del(ctx, arg)
}

func TestArtifactUpdate_InvalidParams(t *testing.T) {
	deps := NewDeps(nil, nil, nil, nil, nil, time.Now, func() string { return "id" })
	res := ArtifactUpdate(context.Background(), deps, NewAuthContext("u1", "user-scoped", "s1"), protocolwire.ArtifactUpdateRequest{})

	ack, ok := res.Ack().(protocolwire.ArtifactAck)
	require.True(t, ok)
	require.Equal(t, "error", ack.Result)
	require.Empty(t, res.Updates())
}

func TestArtifactUpdate_NoUpdatesProvided(t *testing.T) {
	deps := NewDeps(nil, nil, nil, nil, nil, time.Now, func() string { return "id" })
	res := ArtifactUpdate(context.Background(), deps, NewAuthContext("u1", "user-scoped", "s1"), protocolwire.ArtifactUpdateRequest{
		ArtifactID: "a1",
	})

	ack, ok := res.Ack().(protocolwire.ArtifactAck)
	require.True(t, ok)
	require.Equal(t, "error", ack.Result)
	require.Equal(t, "No updates provided", ack.Message)
	require.Empty(t, res.Updates())
}

func TestArtifactUpdate_VersionMismatch(t *testing.T) {
	artifacts := fakeArtifactQueries{
		getByIDAndAccount: func(ctx context.Context, arg models.GetArtifactByIDAndAccountParams) (models.Artifact, error) {
			require.Equal(t, "a1", arg.ID)
			require.Equal(t, "u1", arg.AccountID)
			return models.Artifact{
				ID:            "a1",
				AccountID:     "u1",
				Header:        []byte("h"),
				HeaderVersion: 3,
				Body:          []byte("b"),
				BodyVersion:   5,
			}, nil
		},
		update: func(ctx context.Context, arg models.UpdateArtifactParams) (int64, error) {
			t.Fatalf("unexpected update call")
			return 0, nil
		},
		getByID: func(ctx context.Context, id string) (models.Artifact, error) {
			return models.Artifact{}, sql.ErrNoRows
		},
		create: func(ctx context.Context, arg models.CreateArtifactParams) error {
			return nil
		},
		del: func(ctx context.Context, arg models.DeleteArtifactParams) error {
			return nil
		},
	}
	deps := NewDeps(nil, nil, nil, nil, artifacts, time.Now, func() string { return "id" })

	headerCipher := base64.StdEncoding.EncodeToString([]byte("newh"))
	res := ArtifactUpdate(context.Background(), deps, NewAuthContext("u1", "user-scoped", "sock1"), protocolwire.ArtifactUpdateRequest{
		ArtifactID: "a1",
		Header: &protocolwire.ArtifactUpdatePart{
			Data:            headerCipher,
			ExpectedVersion: 2,
		},
	})

	ack, ok := res.Ack().(protocolwire.ArtifactUpdateMismatchAck)
	require.True(t, ok)
	require.Equal(t, "version-mismatch", ack.Result)
	require.NotNil(t, ack.Header)
	require.Equal(t, int64(3), ack.Header.CurrentVersion)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("h")), ack.Header.CurrentData)
	require.Nil(t, ack.Body)
	require.Empty(t, res.Updates())
}

func TestArtifactUpdate_InvalidEncoding(t *testing.T) {
	artifacts := fakeArtifactQueries{
		getByIDAndAccount: func(ctx context.Context, arg models.GetArtifactByIDAndAccountParams) (models.Artifact, error) {
			return models.Artifact{
				ID:            "a1",
				AccountID:     "u1",
				HeaderVersion: 1,
				BodyVersion:   1,
			}, nil
		},
		update: func(ctx context.Context, arg models.UpdateArtifactParams) (int64, error) {
			t.Fatalf("unexpected update call")
			return 0, nil
		},
		getByID: func(ctx context.Context, id string) (models.Artifact, error) {
			return models.Artifact{}, sql.ErrNoRows
		},
		create: func(ctx context.Context, arg models.CreateArtifactParams) error {
			return nil
		},
		del: func(ctx context.Context, arg models.DeleteArtifactParams) error {
			return nil
		},
	}
	deps := NewDeps(nil, nil, nil, nil, artifacts, time.Now, func() string { return "id" })

	res := ArtifactUpdate(context.Background(), deps, NewAuthContext("u1", "user-scoped", "sock1"), protocolwire.ArtifactUpdateRequest{
		ArtifactID: "a1",
		Header: &protocolwire.ArtifactUpdatePart{
			Data:            "not-base64",
			ExpectedVersion: 1,
		},
	})

	ack, ok := res.Ack().(protocolwire.ArtifactAck)
	require.True(t, ok)
	require.Equal(t, "error", ack.Result)
	require.Equal(t, "Invalid header encoding", ack.Message)
	require.Empty(t, res.Updates())
}

func TestArtifactUpdate_Success(t *testing.T) {
	artifacts := fakeArtifactQueries{
		getByIDAndAccount: func(ctx context.Context, arg models.GetArtifactByIDAndAccountParams) (models.Artifact, error) {
			return models.Artifact{
				ID:            "a1",
				AccountID:     "u1",
				HeaderVersion: 1,
				BodyVersion:   2,
			}, nil
		},
		update: func(ctx context.Context, arg models.UpdateArtifactParams) (int64, error) {
			require.Equal(t, "a1", arg.ID)
			require.Equal(t, "u1", arg.AccountID)
			require.Equal(t, int64(2), arg.HeaderVersion)
			require.Equal(t, int64(3), arg.BodyVersion)
			return 1, nil
		},
		getByID: func(ctx context.Context, id string) (models.Artifact, error) {
			return models.Artifact{}, sql.ErrNoRows
		},
		create: func(ctx context.Context, arg models.CreateArtifactParams) error {
			return nil
		},
		del: func(ctx context.Context, arg models.DeleteArtifactParams) error {
			return nil
		},
	}
	accounts := fakeAccountQueries{
		updateSeq: func(ctx context.Context, id string) (int64, error) {
			return 9, nil
		},
	}
	now := time.UnixMilli(2222)
	deps := NewDeps(accounts, nil, nil, nil, artifacts, func() time.Time { return now }, func() string { return "evt1" })

	headerCipher := base64.StdEncoding.EncodeToString([]byte("h2"))
	bodyCipher := base64.StdEncoding.EncodeToString([]byte("b3"))
	res := ArtifactUpdate(context.Background(), deps, NewAuthContext("u1", "user-scoped", "sock1"), protocolwire.ArtifactUpdateRequest{
		ArtifactID: "a1",
		Header: &protocolwire.ArtifactUpdatePart{
			Data:            headerCipher,
			ExpectedVersion: 1,
		},
		Body: &protocolwire.ArtifactUpdatePart{
			Data:            bodyCipher,
			ExpectedVersion: 2,
		},
	})

	ack, ok := res.Ack().(protocolwire.ArtifactUpdateSuccessAck)
	require.True(t, ok)
	require.Equal(t, "success", ack.Result)
	require.NotNil(t, ack.Header)
	require.Equal(t, int64(2), ack.Header.Version)
	require.Equal(t, headerCipher, ack.Header.Data)
	require.NotNil(t, ack.Body)
	require.Equal(t, int64(3), ack.Body.Version)
	require.Equal(t, bodyCipher, ack.Body.Data)

	require.Len(t, res.Updates(), 1)
	upd := res.Updates()[0]
	require.True(t, upd.IsUser())
	require.Equal(t, "u1", upd.UserID())

	ev := upd.Event()
	require.Equal(t, "evt1", ev.ID)
	require.Equal(t, int64(9), ev.Seq)
	require.Equal(t, now.UnixMilli(), ev.CreatedAt)

	body, ok := ev.Body.(protocolwire.UpdateBodyUpdateArtifact)
	require.True(t, ok)
	require.Equal(t, "update-artifact", body.T)
	require.Equal(t, "a1", body.ArtifactID)
	require.NotNil(t, body.Header)
	require.Equal(t, headerCipher, body.Header.Value)
	require.Equal(t, int64(2), body.Header.Version)
	require.NotNil(t, body.Body)
	require.Equal(t, bodyCipher, body.Body.Value)
	require.Equal(t, int64(3), body.Body.Version)
}
