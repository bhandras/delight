# Delight Encryption Architecture

This document describes the encryption scheme used by the Delight CLI + SDK.
The goal is to keep the implementation **AES‑GCM only** for application payload
encryption, while using NaCl `box` for the QR-code handshake and key wrapping.

## Overview

Delight uses standard primitives available across Go/Swift/Node:

- **AES‑256‑GCM**: encrypts application payloads (session messages + session RPC).
- **NaCl Box (X25519 + XSalsa20‑Poly1305)**: used for QR-code pairing and for
  encrypting/wrapping keys during bootstrap.
- **Ed25519**: authentication (challenge-response) and JWT signing/verification
  (server-side).

## Keys

### `masterSecret` (client root secret)

- 32 bytes (stored as base64 in `~/.delight/master.key`).
- Treated as long-lived client secret material.
- Used to derive a deterministic X25519 keypair (the “content keypair”) for
  decrypting wrapped keys.

### `dataEncryptionKey` (per-session key)

- 32 bytes, per session, stored on the server and returned as base64 of raw key
  bytes.
- Used for AES‑256‑GCM encryption/decryption of **session payloads**.
- The CLI/SDK must hydrate this key (via `GET /v1/sessions` and/or the
  `new-session` update) before decrypting session messages.

## Authentication

The terminal (CLI) authenticates via QR code:

1. CLI generates an ephemeral X25519 keypair (`box.GenerateKey`).
2. CLI sends the public key to the server.
3. Mobile app encrypts a response to that public key and posts it to the server.
4. CLI polls the server and decrypts the response with its private key.

## Wire formats

### AES‑GCM payload envelope (base64 transport)

All AES‑GCM encrypted payloads are transported as base64 of:

`[version=0x00 (1)][nonce (12)][ciphertext+tag (16+)]`

Notes:

- Version byte exists to make upgrades explicit.
- Nonce is 12 bytes (GCM standard).
- Ciphertext includes the authentication tag.

### NaCl Box envelope (binary)

Used for key exchange and QR-code responses:

`[ephemeral public key (32)][nonce (24)][ciphertext+tag]`

## Common failure modes

- `session data key not set (call ListSessions or SetSessionDataKey)`
  - SDK hasn’t hydrated the per-session `dataEncryptionKey` yet.
  - Typical fix: call `ListSessions()` once after auth/connect, or wait for the
    `new-session` update to arrive before decrypting content.

- `unsupported encrypted payload format`
  - The ciphertext isn’t the AES‑GCM envelope described above.
  - If this happens after removing legacy formats, the data in the server DB
    may have been written by an older client.
