package handlers

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// sessionExistsForUser reports whether sessionID exists and belongs to userID.
func sessionExistsForUser(ctx context.Context, deps Deps, sessionID string, userID string) (bool, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(userID) == "" {
		return false, nil
	}

	session, err := deps.Sessions().GetSessionByID(ctx, sessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}

	return session.AccountID == userID, nil
}
