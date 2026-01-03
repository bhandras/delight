-- name: GetTerminal :one
SELECT * FROM terminals
WHERE account_id = ? AND id = ?
LIMIT 1;

-- name: ListTerminals :many
SELECT * FROM terminals
WHERE account_id = ?
ORDER BY last_active_at DESC;

-- name: CreateTerminal :exec
INSERT INTO terminals (
    id, account_id, metadata, metadata_version,
    daemon_state, daemon_state_version, data_encryption_key
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: UpdateTerminalSeq :one
UPDATE terminals
SET seq = seq + 1
WHERE account_id = ? AND id = ?
RETURNING seq;

-- name: UpdateTerminalMetadata :execrows
UPDATE terminals
SET metadata = ?, metadata_version = ?
WHERE account_id = ? AND id = ? AND metadata_version = ?;

-- name: UpdateTerminalDaemonState :execrows
UPDATE terminals
SET daemon_state = ?, daemon_state_version = ?
WHERE account_id = ? AND id = ? AND daemon_state_version = ?;

-- name: UpdateTerminalActivity :exec
UPDATE terminals
SET active = ?, last_active_at = ?
WHERE account_id = ? AND id = ?;

-- name: DeleteTerminal :exec
DELETE FROM terminals WHERE account_id = ? AND id = ?;
