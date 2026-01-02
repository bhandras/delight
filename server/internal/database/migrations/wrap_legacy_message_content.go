package migrations

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/bhandras/delight/shared/logger"
)

// WrapLegacyMessageContent migrates session_messages.content from plain strings to JSON envelopes
// This mirrors the TypeScript server shape: { t: "encrypted", c: <ciphertext> }
func WrapLegacyMessageContent(db *sql.DB) error {
	ctx := context.Background()

	rows, err := db.QueryContext(ctx, `SELECT id, content FROM session_messages WHERE json_valid(content)=0`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type item struct {
		id      string
		content string
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.content); err != nil {
			return err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	logger.Infof("[Migration] Found %d legacy messages with non-JSON content", len(items))

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `UPDATE session_messages SET content=? WHERE id=?`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, it := range items {
		// Wrap legacy string into encrypted envelope
		env, _ := json.Marshal(map[string]any{
			"t": "encrypted",
			"c": it.content,
		})
		if _, err := stmt.ExecContext(ctx, string(env), it.id); err != nil {
			tx.Rollback()
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	logger.Infof("[Migration] Wrapped %d legacy messages", len(items))
	return nil
}
