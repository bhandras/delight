package codexengine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bhandras/delight/cli/internal/agentengine"
	"github.com/bhandras/delight/cli/internal/codex/appserver"
	"github.com/bhandras/delight/shared/wire"
)

// appServerGoalSetResponse is the response payload for thread/goal/set.
type appServerGoalSetResponse struct {
	Goal wire.ThreadGoal `json:"goal"`
}

// appServerGoalGetResponse is the response payload for thread/goal/get.
type appServerGoalGetResponse struct {
	Goal *wire.ThreadGoal `json:"goal"`
}

// appServerGoalClearResponse is the response payload for thread/goal/clear.
type appServerGoalClearResponse struct {
	Cleared bool `json:"cleared"`
}

var _ agentengine.ThreadGoalController = (*Engine)(nil)

// GetThreadGoal returns the current app-server thread goal, if one exists.
func (e *Engine) GetThreadGoal(ctx context.Context) (*wire.ThreadGoal, error) {
	client, threadID, err := e.remoteGoalClientAndThread()
	if err != nil {
		return nil, err
	}
	raw, err := client.Call(ctx, appserver.MethodThreadGoalGet, map[string]any{
		"threadId": threadID,
	})
	if err != nil {
		return nil, err
	}
	var response appServerGoalGetResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	return response.Goal, nil
}

// SetThreadGoal creates or updates the current app-server thread goal.
func (e *Engine) SetThreadGoal(ctx context.Context, objective string, status wire.ThreadGoalStatus) (*wire.ThreadGoal, error) {
	client, threadID, err := e.remoteGoalClientAndThread()
	if err != nil {
		return nil, err
	}
	params := map[string]any{
		"threadId": threadID,
	}
	if trimmed := strings.TrimSpace(objective); trimmed != "" {
		params["objective"] = trimmed
	}
	if status != "" {
		params["status"] = status
	}

	raw, err := client.Call(ctx, appserver.MethodThreadGoalSet, params)
	if err != nil {
		return nil, err
	}
	var response appServerGoalSetResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	return &response.Goal, nil
}

// ClearThreadGoal removes the current app-server thread goal.
func (e *Engine) ClearThreadGoal(ctx context.Context) (bool, error) {
	client, threadID, err := e.remoteGoalClientAndThread()
	if err != nil {
		return false, err
	}
	raw, err := client.Call(ctx, appserver.MethodThreadGoalClear, map[string]any{
		"threadId": threadID,
	})
	if err != nil {
		return false, err
	}
	var response appServerGoalClearResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return false, err
	}
	return response.Cleared, nil
}

// remoteGoalClientAndThread returns the running app-server client and thread id.
func (e *Engine) remoteGoalClientAndThread() (*appserver.Client, string, error) {
	if e == nil {
		return nil, "", fmt.Errorf("codex engine is nil")
	}
	e.mu.Lock()
	client := e.remoteAppServer
	threadID := strings.TrimSpace(e.remoteThreadID)
	enabled := e.remoteEnabled
	e.mu.Unlock()
	if !enabled || client == nil || threadID == "" {
		return nil, "", fmt.Errorf("codex remote thread is not active")
	}
	return client, threadID, nil
}

// handleThreadGoalUpdated emits a compact transcript event for goal changes.
func (e *Engine) handleThreadGoalUpdated(params json.RawMessage) {
	type payload struct {
		ThreadID string          `json:"threadId"`
		Goal     wire.ThreadGoal `json:"goal"`
	}
	var p payload
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	if strings.TrimSpace(p.Goal.ThreadID) == "" {
		p.Goal.ThreadID = strings.TrimSpace(p.ThreadID)
	}
	if strings.TrimSpace(p.Goal.ThreadID) == "" {
		return
	}
	e.tryEmit(agentengine.EvUIEvent{
		Mode:          agentengine.ModeRemote,
		EventID:       goalEventID(p.Goal.ThreadID),
		Kind:          agentengine.UIEventThinking,
		Phase:         agentengine.UIEventPhaseEnd,
		Status:        agentengine.UIEventStatusOK,
		BriefMarkdown: goalBriefMarkdown(&p.Goal),
		FullMarkdown:  goalFullMarkdown(&p.Goal),
		AtMs:          time.Now().UnixMilli(),
	})
}

// handleThreadGoalCleared emits a compact transcript event for goal removal.
func (e *Engine) handleThreadGoalCleared(params json.RawMessage) {
	type payload struct {
		ThreadID string `json:"threadId"`
	}
	var p payload
	_ = json.Unmarshal(params, &p)
	threadID := strings.TrimSpace(p.ThreadID)
	if threadID == "" {
		e.mu.Lock()
		threadID = strings.TrimSpace(e.remoteThreadID)
		e.mu.Unlock()
	}
	if threadID == "" {
		return
	}
	e.tryEmit(agentengine.EvUIEvent{
		Mode:          agentengine.ModeRemote,
		EventID:       goalEventID(threadID),
		Kind:          agentengine.UIEventThinking,
		Phase:         agentengine.UIEventPhaseEnd,
		Status:        agentengine.UIEventStatusOK,
		BriefMarkdown: "Goal cleared",
		FullMarkdown:  "Goal cleared",
		AtMs:          time.Now().UnixMilli(),
	})
}

// goalEventID returns a stable UI event id for goal status updates.
func goalEventID(threadID string) string {
	return "codex-goal:" + strings.TrimSpace(threadID)
}

// goalBriefMarkdown returns a one-line goal status summary.
func goalBriefMarkdown(goal *wire.ThreadGoal) string {
	if goal == nil {
		return "Goal"
	}
	status := goalStatusLabel(goal.Status)
	objective := strings.TrimSpace(goal.Objective)
	if objective == "" {
		return "Goal " + status
	}
	return fmt.Sprintf("Goal %s: %s", status, truncateOneLine(objective, 96))
}

// goalFullMarkdown returns a Markdown goal status summary.
func goalFullMarkdown(goal *wire.ThreadGoal) string {
	if goal == nil {
		return "Goal"
	}
	lines := []string{
		"Goal",
		"",
		"Status: " + goalStatusLabel(goal.Status),
		"Objective: " + strings.TrimSpace(goal.Objective),
		"Time used: " + formatGoalElapsedSeconds(goal.TimeUsedSeconds),
		"Tokens used: " + formatGoalTokens(goal.TokensUsed),
	}
	if goal.TokenBudget != nil {
		lines = append(lines, "Token budget: "+formatGoalTokens(*goal.TokenBudget))
	}
	return strings.Join(lines, "\n")
}

// goalStatusLabel converts upstream status identifiers to user-facing labels.
func goalStatusLabel(status wire.ThreadGoalStatus) string {
	switch status {
	case wire.ThreadGoalStatusActive:
		return "active"
	case wire.ThreadGoalStatusPaused:
		return "paused"
	case wire.ThreadGoalStatusBudgetLimited:
		return "limited by budget"
	case wire.ThreadGoalStatusComplete:
		return "complete"
	default:
		return strings.TrimSpace(string(status))
	}
}

// formatGoalElapsedSeconds formats goal time like Codex's compact TUI display.
func formatGoalElapsedSeconds(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
	const (
		secondsPerMinute = 60
		secondsPerHour   = 60 * secondsPerMinute
		secondsPerDay    = 24 * secondsPerHour
	)
	switch {
	case seconds < secondsPerMinute:
		return fmt.Sprintf("%ds", seconds)
	case seconds < secondsPerHour:
		return fmt.Sprintf("%dm", seconds/secondsPerMinute)
	case seconds < secondsPerDay:
		hours := seconds / secondsPerHour
		minutes := (seconds % secondsPerHour) / secondsPerMinute
		if minutes == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("%dh %dm", hours, minutes)
	default:
		days := seconds / secondsPerDay
		hours := (seconds % secondsPerDay) / secondsPerHour
		minutes := (seconds % secondsPerHour) / secondsPerMinute
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
}

// formatGoalTokens formats large token counts compactly.
func formatGoalTokens(tokens int64) string {
	abs := tokens
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs >= 1_000_000:
		return fmt.Sprintf("%dM", tokens/1_000_000)
	case abs >= 1_000:
		return fmt.Sprintf("%dK", tokens/1_000)
	default:
		return fmt.Sprintf("%d", tokens)
	}
}
