package models

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// SessionTurnState captures durable in-flight turn information for a session.
//
// The server uses this to derive "working" state for clients that reconnect
// after missing ephemeral updates (e.g. when a phone is backgrounded).
type SessionTurnState struct {
	// SessionID is the owning session id.
	SessionID string
	// Open is true when a turn is in-flight.
	Open bool
	// StartedAtMs is the wall-clock timestamp for the most recent turn start.
	StartedAtMs sql.NullInt64
	// CompletedAtMs is the wall-clock timestamp for the most recent turn end.
	CompletedAtMs sql.NullInt64
	// LastUpdatedAtMs is the last time the row was updated (ms since epoch).
	LastUpdatedAtMs int64
}

// EnsureSessionTurnOpen records that the session has an in-flight turn.
//
// If the session is already open, it keeps the original StartedAtMs while
// updating UpdatedAtMs.
func (q *Queries) EnsureSessionTurnOpen(ctx context.Context, sessionID string, atMs int64) error {
	if q == nil || strings.TrimSpace(sessionID) == "" || atMs <= 0 {
		return nil
	}

	_, err := q.db.ExecContext(ctx, `
INSERT INTO session_turn_states (session_id, open, started_at_ms, completed_at_ms, updated_at_ms)
VALUES (?, 1, ?, NULL, ?)
ON CONFLICT(session_id) DO UPDATE SET
	open = 1,
	started_at_ms = CASE
		WHEN session_turn_states.open = 1 THEN session_turn_states.started_at_ms
		ELSE excluded.started_at_ms
	END,
	completed_at_ms = NULL,
	updated_at_ms = excluded.updated_at_ms;
`, sessionID, atMs, atMs)
	return err
}

// EnsureSessionTurnClosed records that the session has no in-flight turn.
//
// If the session was open, CompletedAtMs is updated to atMs. If it was already
// closed, CompletedAtMs is left unchanged (to preserve the last boundary).
func (q *Queries) EnsureSessionTurnClosed(ctx context.Context, sessionID string, atMs int64) error {
	if q == nil || strings.TrimSpace(sessionID) == "" || atMs <= 0 {
		return nil
	}

	_, err := q.db.ExecContext(ctx, `
INSERT INTO session_turn_states (session_id, open, started_at_ms, completed_at_ms, updated_at_ms)
VALUES (?, 0, NULL, ?, ?)
ON CONFLICT(session_id) DO UPDATE SET
	open = 0,
	completed_at_ms = CASE
		WHEN session_turn_states.open = 1 THEN excluded.completed_at_ms
		ELSE session_turn_states.completed_at_ms
	END,
	updated_at_ms = excluded.updated_at_ms;
`, sessionID, atMs, atMs)
	return err
}

// SessionWorkingByID returns whether the session currently has an in-flight turn.
//
// If the session has no recorded turn state, it returns false with no error.
func (q *Queries) SessionWorkingByID(ctx context.Context, sessionID string) (bool, error) {
	if q == nil || strings.TrimSpace(sessionID) == "" {
		return false, nil
	}
	var openInt int64
	err := q.db.QueryRowContext(ctx, `SELECT open FROM session_turn_states WHERE session_id = ?`, sessionID).Scan(&openInt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return openInt != 0, nil
}

// SessionWorkingByIDs returns a map of sessionID -> working for the provided ids.
func (q *Queries) SessionWorkingByIDs(ctx context.Context, sessionIDs []string) (map[string]bool, error) {
	out := make(map[string]bool, len(sessionIDs))
	if q == nil || len(sessionIDs) == 0 {
		return out, nil
	}

	ids := make([]string, 0, len(sessionIDs))
	for _, id := range sessionIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}

	query := fmt.Sprintf(`SELECT session_id, open FROM session_turn_states WHERE session_id IN (%s)`, placeholders)
	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var sessionID string
		var openInt int64
		if err := rows.Scan(&sessionID, &openInt); err != nil {
			return nil, err
		}
		out[sessionID] = openInt != 0
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
