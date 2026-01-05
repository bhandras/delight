-- name: CreateAuthChallenge :exec
INSERT INTO auth_challenges (
    id, public_key, challenge, expires_at
) VALUES (?, ?, ?, ?);

-- name: GetAuthChallenge :one
SELECT id, public_key, challenge, expires_at, created_at
FROM auth_challenges
WHERE id = ? AND public_key = ?
LIMIT 1;

-- name: DeleteAuthChallenge :exec
DELETE FROM auth_challenges
WHERE id = ?;

-- name: DeleteExpiredAuthChallenges :exec
DELETE FROM auth_challenges
WHERE expires_at < CURRENT_TIMESTAMP;

