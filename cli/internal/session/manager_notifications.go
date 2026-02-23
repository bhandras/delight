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
	// pushNotifyTimeout bounds the total time spent on an encrypted push send.
	pushNotifyTimeout = 5 * time.Second
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

// ensurePushNotifier initializes encrypted mobile push notifications if enabled.
func (m *Manager) ensurePushNotifier() {
	if m.push != nil || m.cfg == nil {
		return
	}
	if !m.cfg.PushEnabled() {
		return
	}

	notifier, err := notify.NewPushNotifier(notify.PushConfig{
		ServerURL: m.cfg.ServerURL,
		TokenProvider: func() string {
			return m.token
		},
		MasterSecret: m.masterSecret,
		Cooldown:     m.cfg.PushCooldown,
	})
	if err != nil {
		if m.debug {
			logger.Warnf("Push notifier disabled: %v", err)
		}
		return
	}
	m.push = notifier
}

// notifyTurnComplete emits a Pushover notification for a completed turn.
func (m *Manager) notifyTurnComplete() {
	if !m.isPushoverTurnCompleteEnabled() && !m.isPushTurnCompleteEnabled() {
		return
	}
	ctx := m.buildNotificationContext()
	title := "Delight: Turn finished"
	message := fmt.Sprintf("Agent %s on %s finished a turn in %s.", ctx.agent, ctx.host, ctx.path)
	if m.isPushoverTurnCompleteEnabled() {
		m.sendPushover(pushoverAlertTurnComplete, title, message)
	}
	if m.isPushTurnCompleteEnabled() {
		m.sendPush(notify.PushMessage{
			AlertKey:   pushoverAlertTurnComplete,
			Event:      pushoverAlertTurnComplete,
			Agent:      ctx.agent,
			Host:       ctx.host,
			Path:       ctx.path,
			Label:      "Turn complete",
			SessionID:  m.sessionID,
			SessionTag: m.sessionTag,
			TerminalID: m.terminalID,
			Timestamp:  time.Now().UnixMilli(),
		})
	}
}

// notifyAttention emits a Pushover notification for a permission request.
func (m *Manager) notifyAttention(requestID string, req types.AgentPendingRequest) {
	if !m.isPushoverAttentionEnabled() && !m.isPushAttentionEnabled() {
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
	if m.isPushoverAttentionEnabled() {
		m.sendPushover(alertKey, title, message)
	}
	if m.isPushAttentionEnabled() {
		m.sendPush(notify.PushMessage{
			AlertKey:   alertKey,
			Event:      pushoverAlertAttention,
			Agent:      ctx.agent,
			Host:       ctx.host,
			Path:       ctx.path,
			Label:      "Needs attention",
			SessionID:  m.sessionID,
			SessionTag: m.sessionTag,
			TerminalID: m.terminalID,
			ToolName:   toolName,
			Timestamp:  time.Now().UnixMilli(),
		})
	}
}

// isPushoverTurnCompleteEnabled reports if turn-complete notifications are active.
func (m *Manager) isPushoverTurnCompleteEnabled() bool {
	return m.pushover != nil && m.cfg != nil && m.cfg.PushoverNotifyTurnComplete
}

// isPushoverAttentionEnabled reports if attention notifications are active.
func (m *Manager) isPushoverAttentionEnabled() bool {
	return m.pushover != nil && m.cfg != nil && m.cfg.PushoverNotifyAttention
}

// isPushTurnCompleteEnabled reports if turn-complete push notifications are active.
func (m *Manager) isPushTurnCompleteEnabled() bool {
	return m.push != nil && m.cfg != nil && m.cfg.PushNotifyTurnComplete
}

// isPushAttentionEnabled reports if attention push notifications are active.
func (m *Manager) isPushAttentionEnabled() bool {
	return m.push != nil && m.cfg != nil && m.cfg.PushNotifyAttention
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

// sendPush sends an encrypted push notification, honoring cooldown policies.
func (m *Manager) sendPush(message notify.PushMessage) {
	if m.push == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), pushNotifyTimeout)
	defer cancel()

	if err := m.push.Notify(ctx, message); err != nil {
		logger.Warnf("Push notification failed: %v", err)
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
