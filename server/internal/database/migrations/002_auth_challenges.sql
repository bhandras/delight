-- Auth challenges for /v1/auth (server-issued nonce).
--
-- Challenges are one-time use and short-lived to prevent replay attacks.
-- The server stores the plaintext challenge bytes so it can verify the
-- Ed25519 signature during /v1/auth. Rows are deleted after successful use.

CREATE TABLE IF NOT EXISTS auth_challenges (
    id TEXT PRIMARY KEY,
    public_key TEXT NOT NULL,
    challenge BLOB NOT NULL,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_auth_challenges_public_key ON auth_challenges(public_key);
CREATE INDEX IF NOT EXISTS idx_auth_challenges_expires_at ON auth_challenges(expires_at);

