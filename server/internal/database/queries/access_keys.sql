-- name: GetAccessKey :one
SELECT id, account_id, machine_id, session_id, data, data_version, created_at, updated_at
FROM access_keys
WHERE account_id = ? AND machine_id = ? AND session_id = ?
LIMIT 1;

-- name: CreateAccessKey :exec
INSERT INTO access_keys (
    id, account_id, machine_id, session_id, data, data_version
) VALUES (
    ?, ?, ?, ?, ?, ?
);

-- name: UpdateAccessKey :execrows
UPDATE access_keys
SET data = ?, data_version = ?, updated_at = CURRENT_TIMESTAMP
WHERE account_id = ? AND machine_id = ? AND session_id = ? AND data_version = ?;
