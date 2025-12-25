-- name: CreateMessage :one
INSERT INTO session_messages (
    id, session_id, local_id, seq, content
) VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetMessageByLocalID :one
SELECT * FROM session_messages
WHERE session_id = ? AND local_id = ?
LIMIT 1;

-- name: ListMessages :many
SELECT * FROM session_messages
WHERE session_id = ?
ORDER BY seq ASC
LIMIT ? OFFSET ?;

-- name: GetLatestMessageSeq :one
SELECT COALESCE(MAX(seq), 0) as max_seq
FROM session_messages
WHERE session_id = ?;

-- name: DeleteSessionMessages :exec
DELETE FROM session_messages WHERE session_id = ?;
