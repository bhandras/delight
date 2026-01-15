package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bhandras/delight/cli/internal/notify"
	"github.com/bhandras/delight/cli/pkg/types"
	"github.com/bhandras/delight/shared/logger"
)

const (
	// pushoverAlertTurnComplete identifies a completed turn alert.
	pushoverAlertTurnComplete = "turn-complete"
	// pushoverAlertAttention identifies a needs-attention alert.
	pushoverAlertAttention = "attention"
	// pushoverNotifyTimeout bounds the total time spent on a notification send.
	pushoverNotifyTimeout = 5 * time.Second
)

// notificationContext captures common notification fields derived from session metadata.
type notificationContext struct {
	agent string
	host  string
	path  string
}

// ensurePushoverNotifier initializes Pushover notifications if configured.
func (m *Manager) ensurePushoverNotifier() {
	if m.pushover != nil || m.cfg == nil {
		return
	}
	if !m.cfg.PushoverEnabled() {
		return
	}

	notifier, err := notify.NewPushoverNotifier(notify.PushoverConfig{
		Token:    m.cfg.PushoverToken,
		UserKey:  m.cfg.PushoverUserKey,
		Priority: m.cfg.PushoverPriority,
		Cooldown: m.cfg.PushoverCooldown,
	})
	if err != nil {
		if m.debug {
			logger.Warnf("Pushover notifier disabled: %v", err)
		}
		return
	}
	m.pushover = notifier
}

// notifyTurnComplete emits a Pushover notification for a completed turn.
func (m *Manager) notifyTurnComplete() {
	if !m.isPushoverTurnCompleteEnabled() {
		return
	}
	ctx := m.buildNotificationContext()
	title := "Delight: Turn finished"
	message := fmt.Sprintf("Agent %s on %s finished a turn in %s.", ctx.agent, ctx.host, ctx.path)
	m.sendPushover(pushoverAlertTurnComplete, title, message)
}

// notifyAttention emits a Pushover notification for a permission request.
func (m *Manager) notifyAttention(requestID string, req types.AgentPendingRequest) {
	if !m.isPushoverAttentionEnabled() {
		return
	}
	ctx := m.buildNotificationContext()
	title := "Delight: Needs attention"
	toolName := strings.TrimSpace(req.ToolName)
	message := fmt.Sprintf("Agent %s on %s needs attention in %s.", ctx.agent, ctx.host, ctx.path)
	if toolName != "" {
		message = fmt.Sprintf("Agent %s on %s needs attention for %s in %s.", ctx.agent, ctx.host, toolName, ctx.path)
	}
	alertKey := fmt.Sprintf("%s:%s", pushoverAlertAttention, requestID)
	m.sendPushover(alertKey, title, message)
}

// isPushoverTurnCompleteEnabled reports if turn-complete notifications are active.
func (m *Manager) isPushoverTurnCompleteEnabled() bool {
	return m.pushover != nil && m.cfg != nil && m.cfg.PushoverNotifyTurnComplete
}

// isPushoverAttentionEnabled reports if attention notifications are active.
func (m *Manager) isPushoverAttentionEnabled() bool {
	return m.pushover != nil && m.cfg != nil && m.cfg.PushoverNotifyAttention
}

// sendPushover sends a notification, honoring cooldown policies.
func (m *Manager) sendPushover(alertKey string, title string, message string) {
	if m.pushover == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), pushoverNotifyTimeout)
	defer cancel()

	err := m.pushover.Notify(ctx, notify.PushoverMessage{
		Title:    title,
		Message:  message,
		AlertKey: alertKey,
	})
	if err != nil && m.debug {
		logger.Warnf("Pushover notification failed: %v", err)
	}
}

// buildNotificationContext returns the best-effort context for notifications.
func (m *Manager) buildNotificationContext() notificationContext {
	ctx := notificationContext{
		agent: strings.TrimSpace(m.agent),
		host:  "",
		path:  "",
	}
	if m.metadata != nil {
		ctx.host = strings.TrimSpace(m.metadata.Host)
		ctx.path = strings.TrimSpace(m.metadata.Path)
	}
	if ctx.host == "" {
		ctx.host = "unknown-host"
	}
	if ctx.path == "" {
		ctx.path = strings.TrimSpace(m.workDir)
	}
	if ctx.path == "" {
		ctx.path = "unknown-path"
	}
	if ctx.agent == "" {
		ctx.agent = "unknown-agent"
	}
	return ctx
}

// newPendingRequestIDs returns request IDs present in next but missing from prev.
func newPendingRequestIDs(prev map[string]types.AgentPendingRequest, next map[string]types.AgentPendingRequest) []string {
	if len(next) == 0 {
		return nil
	}
	if len(prev) == 0 {
		ids := make([]string, 0, len(next))
		for id := range next {
			ids = append(ids, id)
		}
		return ids
	}

	ids := make([]string, 0, len(next))
	for id := range next {
		if _, ok := prev[id]; !ok {
			ids = append(ids, id)
		}
	}
	return ids
}
