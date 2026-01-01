-- name: CreateTerminalAuthRequest :one
INSERT INTO terminal_auth_requests (
    id, public_key
) VALUES (?, ?)
RETURNING *;

-- name: GetTerminalAuthRequest :one
SELECT * FROM terminal_auth_requests
WHERE public_key = ?
LIMIT 1;

-- name: UpdateTerminalAuthResponse :exec
UPDATE terminal_auth_requests
SET response = ?, response_account_id = ?
WHERE public_key = ?;

-- name: DeleteOldTerminalAuthRequests :exec
DELETE FROM terminal_auth_requests
WHERE created_at < datetime('now', '-1 hour');

-- name: CreateAccountAuthRequest :one
INSERT INTO account_auth_requests (
    id, public_key
) VALUES (?, ?)
RETURNING *;

-- name: GetAccountAuthRequest :one
SELECT * FROM account_auth_requests
WHERE public_key = ?
LIMIT 1;

-- name: UpdateAccountAuthResponse :exec
UPDATE account_auth_requests
SET response = ?, response_account_id = ?
WHERE public_key = ?;

-- name: DeleteOldAccountAuthRequests :exec
DELETE FROM account_auth_requests
WHERE created_at < datetime('now', '-1 hour');
