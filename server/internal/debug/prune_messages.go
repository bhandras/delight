package debug

import (
	"context"
	"database/sql"
	"log"
)

// PruneMessages deletes all session_messages (dev-only helper).
func PruneMessages(db *sql.DB) error {
	ctx := context.Background()
	res, err := db.ExecContext(ctx, `DELETE FROM session_messages`)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n >= 0 {
		log.Printf("[Debug] Pruned session_messages rows: %d", n)
	}
	return nil
}
