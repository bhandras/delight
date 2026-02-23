package models

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// SessionLastMessageAtByIDs returns latest persisted message timestamps by id.
//
// Timestamps are returned in Unix milliseconds. Sessions without messages are
// omitted from the output map.
func (q *Queries) SessionLastMessageAtByIDs(ctx context.Context, sessionIDs []string) (map[string]int64, error) {
	out := make(map[string]int64, len(sessionIDs))
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

	query := fmt.Sprintf(
		`SELECT session_id, CAST(unixepoch(MAX(created_at)) * 1000 AS INTEGER) AS last_message_at_ms
FROM session_messages
WHERE session_id IN (%s)
GROUP BY session_id`,
		placeholders,
	)
	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var sessionID string
		var lastMessageAtMs sql.NullInt64
		if err := rows.Scan(&sessionID, &lastMessageAtMs); err != nil {
			return nil, err
		}
		if lastMessageAtMs.Valid && lastMessageAtMs.Int64 > 0 {
			out[sessionID] = lastMessageAtMs.Int64
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return out, nil
}
