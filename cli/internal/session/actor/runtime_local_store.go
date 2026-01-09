package actor

import (
	"context"
	"strings"

	framework "github.com/bhandras/delight/cli/internal/actor"
	"github.com/bhandras/delight/cli/internal/storage"
)

// persistLocalSessionInfo persists agent metadata that is only meaningful on the
// local machine (e.g. Codex rollout JSONL path).
func (r *Runtime) persistLocalSessionInfo(ctx context.Context, eff effPersistLocalSessionInfo, emit func(framework.Input)) {
	_ = ctx
	_ = emit

	r.mu.Lock()
	delightHome := strings.TrimSpace(r.delightHome)
	r.mu.Unlock()

	if delightHome == "" {
		return
	}
	if strings.TrimSpace(eff.SessionID) == "" {
		return
	}

	_ = storage.UpdateLocalSessionInfo(delightHome, eff.SessionID, func(info *storage.LocalSessionInfo) {
		if strings.TrimSpace(eff.AgentType) != "" {
			info.AgentType = strings.TrimSpace(eff.AgentType)
		}
		if strings.TrimSpace(eff.ResumeToken) != "" {
			info.ResumeToken = strings.TrimSpace(eff.ResumeToken)
		}
		if strings.TrimSpace(eff.RolloutPath) != "" {
			info.RolloutPath = strings.TrimSpace(eff.RolloutPath)
		}
	})
}
