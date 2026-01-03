-- name: GetSessionByID :one
SELECT * FROM sessions WHERE id = ? LIMIT 1;

-- name: GetSessionByTag :one
SELECT * FROM sessions
WHERE account_id = ? AND tag = ?
LIMIT 1;

-- name: ListSessions :many
SELECT * FROM sessions
WHERE account_id = ?
ORDER BY updated_at DESC
LIMIT ?;

-- name: ListActiveSessions :many
SELECT * FROM sessions
WHERE account_id = ?
  AND active = 1
  AND last_active_at > datetime('now', '-15 minutes')
ORDER BY updated_at DESC
LIMIT ?;

-- name: CreateSession :one
INSERT INTO sessions (
    id, tag, account_id, terminal_id, metadata, metadata_version,
    agent_state, agent_state_version, data_encryption_key
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateSessionSeq :one
UPDATE sessions
SET seq = seq + 1
WHERE id = ?
RETURNING seq;

-- name: UpdateSessionMetadata :execrows
UPDATE sessions
SET metadata = ?, metadata_version = ?
WHERE id = ? AND metadata_version = ?;

-- name: UpdateSessionAgentState :execrows
UPDATE sessions
SET agent_state = ?, agent_state_version = ?
WHERE id = ? AND agent_state_version = ?;

-- name: UpdateSessionActivity :exec
UPDATE sessions
SET active = ?, last_active_at = ?
WHERE id = ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = ?;

-- name: GetSessionMessagesCount :one
SELECT COUNT(*) FROM session_messages WHERE session_id = ?;

-- name: GetSessionLastMessage :one
SELECT * FROM session_messages
WHERE session_id = ?
ORDER BY seq DESC
LIMIT 1;
