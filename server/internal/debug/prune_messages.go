package debug

import (
	"context"
	"database/sql"

	"github.com/bhandras/delight/protocol/logger"
)

// PruneMessages deletes all session_messages (dev-only helper).
func PruneMessages(db *sql.DB) error {
	ctx := context.Background()
	res, err := db.ExecContext(ctx, `DELETE FROM session_messages`)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n >= 0 {
		logger.Infof("[Debug] Pruned session_messages rows: %d", n)
	}
	return nil
}
