package handlers

import (
	"context"

	"github.com/bhandras/delight/server/internal/models"
)

type fakeTerminalQueries struct {
	getTerminal         func(ctx context.Context, arg models.GetTerminalParams) (models.Terminal, error)
	updateTerminalMeta  func(ctx context.Context, arg models.UpdateTerminalMetadataParams) (int64, error)
	updateActivity      func(ctx context.Context, arg models.UpdateTerminalActivityParams) error
	updateTerminalState func(ctx context.Context, arg models.UpdateTerminalDaemonStateParams) (int64, error)
}

func (f fakeTerminalQueries) GetTerminal(ctx context.Context, arg models.GetTerminalParams) (models.Terminal, error) {
	return f.getTerminal(ctx, arg)
}

func (f fakeTerminalQueries) UpdateTerminalMetadata(ctx context.Context, arg models.UpdateTerminalMetadataParams) (int64, error) {
	return f.updateTerminalMeta(ctx, arg)
}

func (f fakeTerminalQueries) UpdateTerminalActivity(ctx context.Context, arg models.UpdateTerminalActivityParams) error {
	if f.updateActivity == nil {
		return nil
	}
	return f.updateActivity(ctx, arg)
}

func (f fakeTerminalQueries) UpdateTerminalDaemonState(ctx context.Context, arg models.UpdateTerminalDaemonStateParams) (int64, error) {
	return f.updateTerminalState(ctx, arg)
}
