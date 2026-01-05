# Delight Protocol: End-to-End Encryption

This document explains Delight’s end-to-end encryption (E2E) at the protocol
level: which keys exist, how they are derived/wrapped, what the wire formats
look like, and what the server is allowed to see.

Scope:

- Only the encryption aspects of the CLI ↔ server ↔ iOS protocol.
- Does **not** cover UI, agent engines, or protocol compatibility with upstream
  “Happy” projects.

## Quick Definitions (Non-Crypto-Friendly)

- **Plaintext**: the readable data (the actual message content).
- **Ciphertext**: encrypted data; looks like random bytes.
- **E2E encryption**: data is encrypted on devices and only decrypted on
  devices. The server stores/relays ciphertext.
- **Account master secret** (`masterSecret`): a 32-byte secret generated on iOS
  and shared to the CLI during pairing. Whoever has it can decrypt that
  account’s E2E data.
- **Wrapped key**: a key that is encrypted so the server can store it, but
  cannot use it. Clients unwrap it locally.
- **Data encryption key** (`dataEncryptionKey`): a per-session/per-terminal/
  per-artifact 32-byte key used to encrypt payloads (messages, RPC, metadata)
  with AES-256-GCM.

## Actors and Trust Model

- **CLI**: runs locally in a directory, produces session messages/events, and
  encrypts them before sending to the server.
- **iOS app**: reads encrypted session data, decrypts locally, and can send
  encrypted RPC/messages back.
- **Server**: stores and relays data and updates. It is treated as **untrusted
  for confidentiality**.

Security goal:

- The server can authenticate/authorize requests and store ciphertext, but it
  must not be able to decrypt session/terminal payloads or the keys used to
  encrypt them.

Non-goal (important):

- E2E encryption does not protect against a compromised device. If an attacker
  can extract the account master secret from the phone or CLI environment, they
  can decrypt that account’s data.

## Two Different “Master Secrets”

There are two different secrets that are easy to confuse:

1. **Account `masterSecret` (E2E root secret)**:
   - Generated on iOS.
   - Stored on iOS + CLI.
   - Used to unwrap wrapped keys and decrypt E2E data.
   - Must remain unknown to the server.
2. **Server `DELIGHT_MASTER_SECRET` (server signing/config secret)**:
   - Only exists on the server as an environment variable.
   - Used to derive the server’s JWT signing key (auth tokens).
   - Does not allow decrypting E2E data by itself.

If someone steals:

- Only the **server DB**: they get ciphertext and wrapped keys, but cannot
  decrypt without the account `masterSecret`.
- The **account `masterSecret`**: they can decrypt that account’s history (DB
  dump or live traffic) and derive the same device keys.

## Key Hierarchy

Delight uses a two-level key hierarchy:

1. **Account master secret** (`masterSecret`): 32 bytes
2. **Per-object data encryption keys** (`dataEncryptionKey`): 32 bytes per
   session (and similarly for terminal/artifact objects)

### 1) `masterSecret` (account root secret)

- **Size**: 32 bytes
- **Where it lives**:
  - CLI: stored on disk in `~/.delight/master.key` (base64 encoded)
  - iOS: stored in Keychain (base64 encoded)
- **What it’s used for**:
  - Derive a deterministic X25519 “content keypair”
  - Unwrap wrapped `dataEncryptionKey` values

Important property:

- The server never receives `masterSecret` in plaintext.

### 2) `dataEncryptionKey` (per-session / per-terminal / per-artifact)

- **Size**: 32 bytes (AES-256 key)
- **What it’s used for**:
  - Encrypt/decrypt payloads for that object using AES-256-GCM.

Important property:

- The server stores these keys only in **wrapped** form (opaque bytes), never
  as plaintext 32-byte keys.

## Deterministic Content Keypair Derivation (X25519)

To wrap/unwrap keys without trusting the server, each account deterministically
derives a NaCl “box” keypair from `masterSecret`.

Inputs:

- `masterSecret` (32 bytes)

Derivation:

- Derive a 32-byte seed via an HMAC-based KDF (see `cli/internal/crypto/derive.go`)
- Interpret that seed as an X25519 private key and **clamp** it (required by
  X25519 / NaCl box expectations).
- Compute public key via `X25519(priv, basepoint)`

Outputs:

- `contentPublicKey` (32 bytes)
- `contentPrivateKey` (32 bytes)

Property:

- Anyone who knows `masterSecret` can derive the same content keypair.
- The server cannot derive this keypair without `masterSecret`.

## Wrapping / Unwrapping `dataEncryptionKey` (NaCl box)

When clients need to store a per-session/per-terminal/per-artifact AES key on
the server, they do not upload the raw 32 bytes. Instead, they **wrap** it
using NaCl box.

### Wrap format on the wire

`wrappedDataEncryptionKey` is transported as:

- `base64( [version=0x00][boxCiphertext...] )`

Where `boxCiphertext` is the NaCl box binary bundle produced by
`EncryptBox`:

- `[ephemeralPublicKey (32)] [nonce (24)] [ciphertext+tag (>=16)]`

So the full decoded byte layout is:

1. `0x00` (1 byte) — wrapper format version
2. `ephemeralPublicKey` (32 bytes)
3. `nonce` (24 bytes)
4. `ciphertext+tag` (variable)

### Wrapping algorithm

To wrap a raw 32-byte `dataEncryptionKey`:

1. Derive `contentPublicKey` from `masterSecret`.
2. Generate an ephemeral NaCl box keypair.
3. Generate a random 24-byte nonce.
4. Encrypt the 32-byte key with NaCl box:
   - recipient public key = `contentPublicKey`
   - sender private key = ephemeral private key
5. Emit the bundle as described above and base64 encode it for JSON transport.

### Unwrapping algorithm

To unwrap a wrapped key:

1. Base64 decode to bytes.
2. Verify `version == 0x00`.
3. Derive `contentPrivateKey` from `masterSecret`.
4. Parse `(ephemeralPublicKey, nonce, ciphertext)` and call `box.Open`.
5. The result must be **exactly 32 bytes** (the raw AES key).

## Payload Encryption (AES-256-GCM)

Once a client has the raw `dataEncryptionKey` (32 bytes), it encrypts payloads
using AES-256-GCM.

### AES-GCM envelope format

Encrypted payloads are transported as base64 of:

`[version=0x00 (1)] [nonce (12)] [ciphertext+tag (>=16)]`

Notes:

- The version byte makes format upgrades explicit.
- Nonce is 12 bytes (standard for GCM).
- Ciphertext includes the authentication tag appended by GCM.

### What gets encrypted

Anything that should be confidential from the server is encrypted in an
AES-GCM envelope and sent/stored as ciphertext. This includes:

- Session messages (user/agent transcript items)
- Session-scoped RPC payloads
- Terminal metadata / daemon state (where those are treated as private)

The server stores these payloads as base64 strings / JSON blobs and relays them
over HTTP and Socket.IO, but does not inspect plaintext.

## How the Server Handles Encrypted Data

The server’s responsibilities are intentionally limited:

- Validate authentication/authorization (JWT)
- Store bytes and JSON blobs
- Fan out updates over Socket.IO

The server **does not**:

- Hold `masterSecret`
- Unwrap `dataEncryptionKey`
- Decrypt AES-GCM payloads
- Generate `dataEncryptionKey` values on behalf of clients

To limit abuse, the server may apply basic validation:

- Base64 decode must succeed for `dataEncryptionKey` fields
- Wrapped key size must be within a sane bound (to avoid memory abuse)

## End-to-End Flows (Encryption Only)

### A) Terminal pairing: bootstrap the master secret (NaCl box)

Purpose: get the same `masterSecret` onto both iOS and CLI without typing it.

1. CLI generates an ephemeral NaCl box keypair (X25519).
2. CLI shows a QR containing the ephemeral public key.
3. iOS encrypts the 32-byte `masterSecret` to that public key using NaCl box
   and submits the ciphertext to the server.
4. CLI polls and decrypts the ciphertext with its ephemeral private key.

At the end:

- Both devices share `masterSecret`.
- No long-lived JWT is transferred via the pairing poll response.

What the server sees:

- The QR public key (not secret).
- The encrypted response blob (ciphertext).
- It cannot decrypt the `masterSecret`.

### B) Authenticate to the server (JWT) without leaking secrets

Purpose: obtain a JWT for API access without allowing replay.

1. Client derives a deterministic Ed25519 signing keypair from `masterSecret`.
2. Client requests a one-time challenge from `POST /v1/auth/challenge`.
3. Server returns:
   - a random challenge (bytes, base64)
   - a `challengeId`
4. Client signs the challenge bytes and calls `POST /v1/auth` with:
   - `publicKey`
   - `challengeId`
   - `signature`
5. Server verifies signature for that `challengeId`, deletes the challenge, and
   returns a short-lived JWT.

What the server sees:

- The public key and signature.
- The challenge bytes (server-generated).
- The server still does not learn the account `masterSecret`.

### C) Session create: publish wrapped data key

1. CLI generates a random 32-byte `dataEncryptionKey` for the session.
2. CLI wraps it with the content keypair derived from `masterSecret`.
3. CLI sends `dataEncryptionKey` (wrapped, base64) to the server in the create
   session request.

iOS later:

4. Fetches sessions, receives wrapped key from server, unwraps locally using
   the same `masterSecret`.

What the server sees:

- The wrapped `dataEncryptionKey` bytes (opaque bytes).
- The server cannot unwrap them without the account `masterSecret`.

### D) Message/RPC encryption and decryption

1. Sender (CLI or iOS) encrypts plaintext payload with AES-256-GCM under the
   session’s raw 32-byte `dataEncryptionKey`.
2. Sender transmits ciphertext envelope (base64) via HTTP/Socket.IO.
3. Receiver base64-decodes and decrypts locally using the same key.

What the server sees:

- Encrypted envelopes (ciphertext) and metadata necessary for routing.
- It cannot read message content.

## Practical Implications / Debugging Notes

- If a client cannot decrypt messages after reconnect:
  - It likely has not hydrated (or cannot unwrap) the session’s wrapped
    `dataEncryptionKey` yet.
- If decryption fails consistently:
  - The `masterSecret` differs between devices (pairing mismatch), or
  - The wrapped key stored on the server does not correspond to the session’s
    encryption key (data corruption), or
  - The ciphertext is not an AES-GCM envelope (legacy/invalid data).

## What “Data Is Safe” Means Here

In this design, “safe” means:

- A server operator (or DB breach) cannot decrypt session content.
- A network observer cannot decrypt session content.

It does not mean:

- A compromised phone/laptop can’t decrypt. If an attacker can extract the
  account `masterSecret`, they can decrypt that account’s data.

To protect tokens and reduce tampering risk, run the server behind TLS (HTTPS),
even though confidentiality of message content is E2E.

## References in Code

- X25519 content keypair derivation:
  - `cli/internal/crypto/derive.go`
- NaCl box wrap/unwrap:
  - `cli/internal/crypto/box.go`
  - `cli/internal/crypto/derive.go` (`EncryptDataEncryptionKey`,
    `DecryptDataEncryptionKey`)
- AES-GCM envelope:
  - `cli/internal/crypto/aesgcm.go`
- Server key storage semantics (opaque wrapped keys):
  - `server/internal/api/handlers/sessions.go`
  - `server/internal/api/handlers/terminals.go`
  - `server/internal/api/handlers/artifacts.go`
