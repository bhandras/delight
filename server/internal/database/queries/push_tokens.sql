-- name: CreatePushToken :one
INSERT INTO account_push_tokens (
    id, account_id, token
) VALUES (?, ?, ?)
ON CONFLICT(account_id, token) DO UPDATE SET updated_at = CURRENT_TIMESTAMP
RETURNING *;

-- name: ListPushTokens :many
SELECT * FROM account_push_tokens
WHERE account_id = ?;

-- name: DeletePushToken :exec
DELETE FROM account_push_tokens
WHERE account_id = ? AND token = ?;
