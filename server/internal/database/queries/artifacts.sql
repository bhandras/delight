-- name: ListArtifactsByAccount :many
SELECT id, account_id, header, header_version, data_encryption_key, seq, created_at, updated_at
FROM artifacts
WHERE account_id = ?
ORDER BY updated_at DESC;

-- name: GetArtifactByID :one
SELECT id, account_id, header, header_version, body, body_version, data_encryption_key, seq, created_at, updated_at
FROM artifacts
WHERE id = ?
LIMIT 1;

-- name: GetArtifactByIDAndAccount :one
SELECT id, account_id, header, header_version, body, body_version, data_encryption_key, seq, created_at, updated_at
FROM artifacts
WHERE id = ? AND account_id = ?
LIMIT 1;

-- name: CreateArtifact :exec
INSERT INTO artifacts (
    id, account_id, header, header_version, body, body_version, data_encryption_key, seq
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?
);

-- name: UpdateArtifact :execrows
UPDATE artifacts
SET
    header = COALESCE(?, header),
    header_version = CASE WHEN ? = 1 THEN ? ELSE header_version END,
    body = COALESCE(?, body),
    body_version = CASE WHEN ? = 1 THEN ? ELSE body_version END,
    seq = seq + 1,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND account_id = ?
  AND (? = 0 OR header_version = ?)
  AND (? = 0 OR body_version = ?);

-- name: DeleteArtifact :exec
DELETE FROM artifacts
WHERE id = ? AND account_id = ?;
