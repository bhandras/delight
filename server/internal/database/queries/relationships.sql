-- name: GetRelationship :one
SELECT from_user_id, to_user_id, status, created_at, updated_at, accepted_at, last_notified_at
FROM user_relationships
WHERE from_user_id = ? AND to_user_id = ?
LIMIT 1;

-- name: UpsertRelationship :exec
INSERT INTO user_relationships (
    from_user_id, to_user_id, status, accepted_at, last_notified_at
) VALUES (
    ?, ?, ?, ?, ?
)
ON CONFLICT(from_user_id, to_user_id)
DO UPDATE SET
    status = excluded.status,
    accepted_at = excluded.accepted_at,
    last_notified_at = COALESCE(excluded.last_notified_at, user_relationships.last_notified_at),
    updated_at = CURRENT_TIMESTAMP;

-- name: ListRelationshipsByFrom :many
SELECT from_user_id, to_user_id, status, created_at, updated_at, accepted_at, last_notified_at
FROM user_relationships
WHERE from_user_id = ? AND status IN ('friend', 'pending', 'requested');
