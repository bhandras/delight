package handlers

import (
	"context"
	"time"

	"github.com/bhandras/delight/server/internal/models"
)

// AccountQueries is the subset of account queries used by websocket handlers.
type AccountQueries interface {
	UpdateAccountSeq(ctx context.Context, id string) (int64, error)
}

// SessionQueries is the subset of session queries used by websocket handlers.
type SessionQueries interface {
	GetSessionByID(ctx context.Context, id string) (models.Session, error)
	UpdateSessionAgentState(ctx context.Context, arg models.UpdateSessionAgentStateParams) (int64, error)
	UpdateSessionActivity(ctx context.Context, arg models.UpdateSessionActivityParams) error
	UpdateSessionMetadata(ctx context.Context, arg models.UpdateSessionMetadataParams) (int64, error)
}

// MachineQueries is the subset of machine queries used by websocket handlers.
type MachineQueries interface {
	GetMachine(ctx context.Context, arg models.GetMachineParams) (models.Machine, error)
	UpdateMachineMetadata(ctx context.Context, arg models.UpdateMachineMetadataParams) (int64, error)
	UpdateMachineActivity(ctx context.Context, arg models.UpdateMachineActivityParams) error
	UpdateMachineDaemonState(ctx context.Context, arg models.UpdateMachineDaemonStateParams) (int64, error)
}

// AccessKeyQueries is the subset of access key queries used by websocket handlers.
type AccessKeyQueries interface {
	GetAccessKey(ctx context.Context, arg models.GetAccessKeyParams) (models.AccessKey, error)
}

// ArtifactQueries is the subset of artifact queries used by websocket handlers.
type ArtifactQueries interface {
	GetArtifactByID(ctx context.Context, id string) (models.Artifact, error)
	GetArtifactByIDAndAccount(ctx context.Context, arg models.GetArtifactByIDAndAccountParams) (models.Artifact, error)
	CreateArtifact(ctx context.Context, arg models.CreateArtifactParams) error
	UpdateArtifact(ctx context.Context, arg models.UpdateArtifactParams) (int64, error)
	DeleteArtifact(ctx context.Context, arg models.DeleteArtifactParams) error
}

// Deps holds the narrow dependencies required by extracted websocket handlers.
type Deps struct {
	accounts  AccountQueries
	sessions  SessionQueries
	machines  MachineQueries
	accessKey AccessKeyQueries
	artifacts ArtifactQueries
	now       func() time.Time
	newID     func() string
}

// NewDeps builds a dependency bundle for handler calls.
func NewDeps(
	accounts AccountQueries,
	sessions SessionQueries,
	machines MachineQueries,
	accessKeys AccessKeyQueries,
	artifacts ArtifactQueries,
	now func() time.Time,
	newID func() string,
) Deps {
	return Deps{
		accounts:  accounts,
		sessions:  sessions,
		machines:  machines,
		accessKey: accessKeys,
		artifacts: artifacts,
		now:       now,
		newID:     newID,
	}
}

func (d Deps) Accounts() AccountQueries { return d.accounts }
func (d Deps) Sessions() SessionQueries { return d.sessions }
func (d Deps) Machines() MachineQueries { return d.machines }
func (d Deps) AccessKeys() AccessKeyQueries {
	return d.accessKey
}
func (d Deps) Artifacts() ArtifactQueries { return d.artifacts }
func (d Deps) Now() time.Time {
	if d.now != nil {
		return d.now()
	}
	return time.Now()
}
func (d Deps) NewID() string {
	if d.newID != nil {
		return d.newID()
	}
	return ""
}
