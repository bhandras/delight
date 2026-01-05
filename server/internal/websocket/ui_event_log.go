package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	pkgtypes "github.com/bhandras/delight/server/pkg/types"
	"github.com/bhandras/delight/shared/logger"
	socket "github.com/zishang520/socket.io/servers/socket/v3"
)

// uiEventLogReplayLimit caps how many stored UI events are replayed when a
// user-scoped socket reconnects.
const uiEventLogReplayLimit = 500

// uiEventLogMaxRowsPerAccount caps how many UI event rows are retained per
// account before pruning oldest entries.
const uiEventLogMaxRowsPerAccount = 2_000

// maybeStoreUIEventLogEntry persists a `ui.event` ephemeral payload for later
// replay. Non-UI-event ephemerals are ignored.
func (s *SocketIOServer) maybeStoreUIEventLogEntry(ctx context.Context, userID string, payload map[string]any) {
	eventType, _ := payload["type"].(string)
	if eventType != "ui.event" {
		return
	}

	sessionID, _ := payload["sessionId"].(string)
	if sessionID == "" {
		// Without a session id the mobile UI cannot associate the event.
		return
	}

	atMs := coerceInt64(payload["atMs"])
	if atMs <= 0 {
		atMs = time.Now().UnixMilli()
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}

	if err := s.insertUIEventLogRow(ctx, userID, sessionID, atMs, string(raw)); err != nil {
		logger.Debugf("ui-event log insert failed: %v", err)
	}
}

// replayUIEventLog re-emits stored UI event ephemerals on a user-scoped socket
// connect so mobile clients can catch up after being backgrounded.
func (s *SocketIOServer) replayUIEventLog(ctx context.Context, userID string, client *socket.Socket) {
	if userID == "" || client == nil {
		return
	}

	rows, err := s.loadUIEventLogRows(ctx, userID, uiEventLogReplayLimit)
	if err != nil {
		logger.Debugf("ui-event log load failed: %v", err)
		return
	}

	for _, payload := range rows {
		client.Emit("ephemeral", payload)
	}
}

// insertUIEventLogRow inserts a single UI event log row and prunes older rows
// for the same user.
func (s *SocketIOServer) insertUIEventLogRow(ctx context.Context, userID, sessionID string, atMs int64, payloadJSON string) error {
	if s.db == nil {
		return fmt.Errorf("db not configured")
	}
	if userID == "" || sessionID == "" || payloadJSON == "" || atMs <= 0 {
		return fmt.Errorf("missing required fields")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO session_ui_events (id, account_id, session_id, payload, at_ms)
         VALUES (?, ?, ?, ?, ?)`,
		pkgtypes.NewCUID(),
		userID,
		sessionID,
		payloadJSON,
		atMs,
	)
	if err != nil {
		return err
	}

	// Prune oldest entries beyond the retention cap. This keeps the feature
	// bounded even if a session emits many UI events.
	_, _ = tx.ExecContext(
		ctx,
		`DELETE FROM session_ui_events
         WHERE id IN (
           SELECT id FROM session_ui_events
           WHERE account_id = ?
           ORDER BY at_ms DESC
           LIMIT -1 OFFSET ?
         )`,
		userID,
		uiEventLogMaxRowsPerAccount,
	)

	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// loadUIEventLogRows returns the last `limit` stored UI event payloads for the
// given user in chronological order.
func (s *SocketIOServer) loadUIEventLogRows(ctx context.Context, userID string, limit int) ([]map[string]any, error) {
	if s.db == nil {
		return nil, fmt.Errorf("db not configured")
	}
	if userID == "" {
		return nil, fmt.Errorf("userID required")
	}
	if limit <= 0 {
		return nil, nil
	}

	// Load newest rows, then reverse into chronological order before replay.
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT payload FROM session_ui_events
         WHERE account_id = ?
         ORDER BY at_ms DESC
         LIMIT ?`,
		userID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var payloads []map[string]any
	for rows.Next() {
		var payloadJSON string
		if err := rows.Scan(&payloadJSON); err != nil {
			return nil, err
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(payloadJSON), &decoded); err != nil {
			continue
		}
		payloads = append(payloads, decoded)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Reverse newest-first into oldest-first so UI events replay in order.
	for i, j := 0, len(payloads)-1; i < j; i, j = i+1, j-1 {
		payloads[i], payloads[j] = payloads[j], payloads[i]
	}
	return payloads, nil
}

// coerceInt64 normalizes values that may have been decoded from JSON into an
// int64 representation.
func coerceInt64(value any) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n
		}
	}
	return 0
}
