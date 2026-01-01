# AES‑GCM Only (Session Payload Encryption)

This document tracks the plan to use a **single** encryption scheme for
**session payloads** (messages + session RPC), and to remove the legacy
SecretBox-based scheme from the session path.

There are no production users/clients to maintain backwards compatibility for,
so we can simplify aggressively.

## Goal

- Use **AES‑256‑GCM** exclusively for session payload encryption/decryption.
- Use a **per-session** 32‑byte key (`dataEncryptionKey`) stored on the server.
- Ensure all clients (CLI, iOS harness) can always obtain the session key.

## Non‑Goals

- This doc does not (yet) migrate machine-scoped RPC encryption or other
  non-session payloads.
- This doc does not address transport security (TLS), only application-layer
  encryption at rest/in transit through the server DB.

## Terminology

- **Session key**: `dataEncryptionKey` (32 bytes), per session.
- **AES‑GCM payload format** (binary, before base64):
  - `[version=0x00 (1 byte)]`
  - `[nonce (12 bytes)]`
  - `[ciphertext + tag (16 bytes tag)]`
- **Ciphertext transport**: Base64-encoded AES‑GCM payload.

## Why AES‑GCM

- Standard, widely available across Go/Swift/Node/WebCrypto.
- Simple to implement in standard libraries.
- Fast on modern CPUs (AES acceleration common).

## Key invariants (must always hold)

- A session must always have a non-empty `dataEncryptionKey`.
- The CLI must load `dataEncryptionKey` before attempting to decrypt session
  message payloads.
- If the key is missing, we fail fast with a clear error (no silent fallback).

## Design

### Server responsibilities

- Ensure every session row has `sessions.data_encryption_key` populated.
  - If a session is created without a key, generate one.
  - If a session is reused (stable tag) and is missing a key (unexpected),
    generate and persist one.
- Include `dataEncryptionKey` in `GET /v1/sessions` responses (base64 of the
  raw 32 bytes).
- Include `dataEncryptionKey` in `POST /v1/sessions` responses.

### CLI responsibilities

- Generate a session key during session creation and send it to the server
  (best-effort; the server may already have a key for an existing session).
- Load the session key from the create-session response when present.
- Use AES‑GCM only for `Manager.encrypt`/`Manager.decrypt` session payloads.
- Treat non-AES‑GCM session ciphertext as unsupported.

### Test responsibilities

- Integration tests must fetch the session `dataEncryptionKey` from
  `GET /v1/sessions` and encrypt/decrypt updates using AES‑GCM.

## Milestones / TODO

- [x] Server: ensure `dataEncryptionKey` always exists for sessions.
- [x] Protocol: model `dataEncryptionKey` in wire types.
- [x] CLI: AES‑GCM only for session payloads; fail fast if key missing.
- [x] CLI: hydrate key from create-session response and `new-session` update.
- [x] Tests: update itests to use `dataEncryptionKey` + AES‑GCM.
- [x] Remove legacy SecretBox implementation and call sites.
