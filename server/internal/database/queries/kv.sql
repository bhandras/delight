-- name: ListKV :many
SELECT id, account_id, key, value, version, created_at, updated_at
FROM user_kv_store
WHERE account_id = ?
  AND (? = '' OR key LIKE ? || '%')
ORDER BY key
LIMIT ?;

-- name: GetKV :one
SELECT id, account_id, key, value, version, created_at, updated_at
FROM user_kv_store
WHERE account_id = ? AND key = ?
LIMIT 1;

-- name: UpsertKV :exec
INSERT INTO user_kv_store (id, account_id, key, value, version, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
ON CONFLICT(account_id, key)
DO UPDATE SET
    value = excluded.value,
    version = excluded.version,
    updated_at = CURRENT_TIMESTAMP
WHERE user_kv_store.version = excluded.version - 1;

-- name: DeleteKV :exec
DELETE FROM user_kv_store
WHERE account_id = ? AND key = ? AND version = ? - 1;
