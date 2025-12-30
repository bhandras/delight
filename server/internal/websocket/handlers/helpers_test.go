package handlers

import (
	"context"

	"github.com/bhandras/delight/server/internal/models"
)

type fakeMachineQueries struct {
	getMachine         func(ctx context.Context, arg models.GetMachineParams) (models.Machine, error)
	updateMachineMeta  func(ctx context.Context, arg models.UpdateMachineMetadataParams) (int64, error)
	updateActivity     func(ctx context.Context, arg models.UpdateMachineActivityParams) error
	updateMachineState func(ctx context.Context, arg models.UpdateMachineDaemonStateParams) (int64, error)
}

func (f fakeMachineQueries) GetMachine(ctx context.Context, arg models.GetMachineParams) (models.Machine, error) {
	return f.getMachine(ctx, arg)
}

func (f fakeMachineQueries) UpdateMachineMetadata(ctx context.Context, arg models.UpdateMachineMetadataParams) (int64, error) {
	return f.updateMachineMeta(ctx, arg)
}

func (f fakeMachineQueries) UpdateMachineActivity(ctx context.Context, arg models.UpdateMachineActivityParams) error {
	if f.updateActivity == nil {
		return nil
	}
	return f.updateActivity(ctx, arg)
}

func (f fakeMachineQueries) UpdateMachineDaemonState(ctx context.Context, arg models.UpdateMachineDaemonStateParams) (int64, error) {
	return f.updateMachineState(ctx, arg)
}

type fakeAccessKeyQueries struct {
	get func(ctx context.Context, arg models.GetAccessKeyParams) (models.AccessKey, error)
}

func (f fakeAccessKeyQueries) GetAccessKey(ctx context.Context, arg models.GetAccessKeyParams) (models.AccessKey, error) {
	return f.get(ctx, arg)
}
