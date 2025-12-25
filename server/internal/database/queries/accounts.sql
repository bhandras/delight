-- name: GetAccountByID :one
SELECT * FROM accounts WHERE id = ? LIMIT 1;

-- name: GetAccountByPublicKey :one
SELECT * FROM accounts WHERE public_key = ? LIMIT 1;

-- name: GetAccountByUsername :one
SELECT * FROM accounts WHERE username = ? LIMIT 1;

-- name: CreateAccount :one
INSERT INTO accounts (
    id, public_key
) VALUES (?, ?)
RETURNING *;

-- name: UpdateAccountSeq :one
UPDATE accounts
SET seq = seq + 1
WHERE id = ?
RETURNING seq;

-- name: UpdateAccountFeedSeq :one
UPDATE accounts
SET feed_seq = feed_seq + 1
WHERE id = ?
RETURNING feed_seq;

-- name: UpdateAccountSettings :exec
UPDATE accounts
SET settings = ?, settings_version = ?
WHERE id = ? AND settings_version = ?;

-- name: UpdateAccountProfile :exec
UPDATE accounts
SET first_name = ?, last_name = ?, username = ?
WHERE id = ?;

-- name: UpdateAccountAvatar :exec
UPDATE accounts
SET avatar_url = ?, avatar_width = ?, avatar_height = ?, avatar_thumbhash = ?
WHERE id = ?;

-- name: DeleteAccountAvatar :exec
UPDATE accounts
SET avatar_url = NULL, avatar_width = NULL, avatar_height = NULL, avatar_thumbhash = NULL
WHERE id = ?;
