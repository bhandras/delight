# Security Review (Delight)

This document is a practical security review of Delight’s current
CLI ↔ server ↔ iOS protocol and storage model.

It is written for the current state of the repo (not for compatibility with
the upstream “Happy” projects this was inspired by).

## Threat Model (What We’re Defending Against)

- The **server is untrusted for confidentiality**: assume it can be read by an
  attacker (database + logs), and assume it can be run by a curious operator.
- Network attackers can observe/modify traffic unless TLS is used end-to-end.
- The iOS app and CLI are trusted on the user’s devices (but should still avoid
  leaking secrets into logs).

## High-Level Goals

1. **End-to-end encryption (E2E)** of session/terminal payloads:
   the server should not be able to decrypt message content or keys.
2. **No replayable authentication**:
   server-issued challenges must be one-time and expire quickly.
3. **No token leakage** in HTTP or WebSocket logs.
4. Reduce browser attack surface via safer CORS defaults.

## Findings + Fixes Implemented (protocol-fixes)

### 1) Server stored plaintext data keys (breaks “E2E”)

**Risk**
- If the server stores per-session (or per-terminal/artifact) AES keys in
  plaintext, the server (or anyone with DB access) can decrypt content.

**Fix**
- The server now stores `dataEncryptionKey` as **opaque wrapped bytes**:
  clients encrypt (wrap) the 32-byte key using NaCl box with a deterministic
  X25519 “content keypair” derived from the account master secret.
- The server:
  - accepts `dataEncryptionKey` as base64 of opaque bytes (bounded in size),
  - stores it as-is,
  - returns it to clients as-is,
  - never generates or decrypts it.

### 2) Pairing poll endpoint leaked JWTs

**Risk**
- `GET /v1/auth/request/status` returning a JWT allows anyone who can see the
  response (logs, MITM, reverse proxy logs, etc.) to authenticate.

**Fix**
- The status endpoint now returns only the encrypted pairing response; the
  CLI derives identity and authenticates separately to obtain a token.

### 3) Replayable /v1/auth challenge

**Risk**
- A fixed or client-supplied challenge enables replay: an attacker can re-send
  the same signed payload to get a token indefinitely.

**Fix**
- Added `POST /v1/auth/challenge`:
  - server issues a random 32-byte nonce and stores it with a short TTL,
  - client signs the nonce bytes,
  - `POST /v1/auth` verifies the signature using the stored nonce and deletes
    the challenge on success (one-time use).

### 4) Long-lived JWTs (no expiry)

**Risk**
- A leaked token remains valid forever.

**Fix**
- JWTs now include an `exp` claim (currently 24h TTL). Expiry is validated by
  JWT verification.

### 5) CORS wildcard + credentials

**Risk**
- `Access-Control-Allow-Origin: *` with credentials is unsafe and invalid for
  browsers. Even if the iOS app isn’t a browser, leaving this enabled invites
  accidental exposure if a web UI or embedding is added later.

**Fix**
- HTTP CORS only enables `AllowCredentials` when origins are not wildcard.
- Socket.IO CORS no longer sets `credentials: true` when using `Origin: *`.

### 6) Token leakage via WebSocket handshake logging

**Risk**
- Logging the handshake `auth` payload leaks bearer tokens to disk.

**Fix**
- Socket.IO connection logging no longer dumps handshake auth/headers; logs are
  sanitized to exclude bearer tokens.

## Remaining Recommendations / TODOs

These are not required for basic correctness but are strongly recommended if
you plan to host the server publicly:

1. **TLS everywhere**
   - Prefer terminating TLS via a reverse proxy (Caddy/Nginx) and use HTTPS for
     all clients.
2. **Token refresh / rotation**
   - Short-lived access tokens are good; pair with a refresh mechanism or
     re-auth flow (master-secret based) to avoid prompting users frequently.
3. **Rate limiting**
   - Add per-IP / per-account limits to `/v1/auth/challenge`, `/v1/auth`,
     and pairing poll endpoints to slow brute force.
4. **Origin allowlist**
   - If a web UI is ever added, replace `AllowedOrigins=["*"]` with an explicit
     allowlist and keep credentials enabled only for those origins.
5. **Secrets-at-rest**
   - Ensure tokens and master secrets on disk are stored with restrictive
     permissions (already enforced in most places, but should be audited).

