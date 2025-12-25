# Delight Encryption Architecture

This document explains the encryption scheme used between the Delight mobile app and CLI, including the V1/V2 key formats and known issues.

## Overview

Delight uses end-to-end encryption for all sensitive data. The encryption is based on NaCl/libsodium primitives:

- **SecretBox** (XSalsa20-Poly1305): Symmetric encryption for messages
- **Box** (X25519 + XSalsa20-Poly1305): Asymmetric encryption for key exchange
- **AES-256-GCM**: Used in V2 scheme for per-record encryption (partially implemented)

## Key Hierarchy

```
Account Creation (Mobile App)
           │
           ▼
┌─────────────────────────────────────────────────────────────────┐
│                      masterSecret (32 bytes)                     │
│         Generated once per account, stored on mobile app         │
│              Used for V1 SecretBox encryption                    │
└─────────────────────────────────────────────────────────────────┘
           │
           │ HMAC-SHA512 derive('Delight EnCoder', ['content'])
           ▼
┌─────────────────────────────────────────────────────────────────┐
│                    contentDataKey (32 bytes)                     │
│                  Derived deterministically                       │
└─────────────────────────────────────────────────────────────────┘
           │
           │ crypto_box_seed_keypair()
           ▼
┌─────────────────────────────────────────────────────────────────┐
│                      contentKeyPair                              │
│         publicKey (32 bytes) + privateKey (32 bytes)            │
│      Used for V2 Box encryption of per-record keys              │
└─────────────────────────────────────────────────────────────────┘
```

## Authentication Flow

When a CLI authenticates via QR code scanning:

1. CLI generates ephemeral Box keypair
2. CLI sends `publicKey` to server with `supportsV2` flag
3. Mobile app scans QR code and encrypts response with CLI's public key
4. Server relays encrypted response to CLI
5. CLI decrypts to get the account secret

### V1 Format (Current - Used by CLI)

```
Mobile sends: Box.encrypt(masterSecret, cliPublicKey)
CLI receives: 32 bytes (raw masterSecret)
```

The CLI uses `masterSecret` directly for SecretBox encryption/decryption.

### V2 Format (Partially Implemented - NOT used by CLI)

```
Mobile sends: Box.encrypt([0x00] + contentKeyPair.publicKey, cliPublicKey)
CLI receives: 33 bytes ([version=0x00][publicKey])
```

The V2 format sends `contentKeyPair.publicKey` which is intended for:
- Decrypting per-record `dataEncryptionKey`s stored in the database
- Each session/machine could have its own AES key wrapped with this public key

**Important**: V2 is NOT suitable for CLI because:
1. Mobile app still uses `legacyEncryption` (SecretBox + masterSecret) for actual messages
2. The `contentKeyPair.publicKey` cannot decrypt SecretBox messages
3. CLI would need the full V2 infrastructure to use per-record keys

## Encryption Formats

### SecretBox Format (V1 - Current)

Used for encrypting messages between mobile and CLI.

```
┌──────────────────────┬────────────────────────────────────────┐
│   Nonce (24 bytes)   │     Ciphertext + Auth Tag (16 bytes)   │
└──────────────────────┴────────────────────────────────────────┘
```

- **Nonce**: Random 24 bytes, prepended to ciphertext
- **Ciphertext**: XSalsa20 encrypted data
- **Auth Tag**: Poly1305 MAC (16 bytes, appended by libsodium)
- **Key**: `masterSecret` (32 bytes)

### Box Format (Key Exchange)

Used for encrypting secrets during authentication.

```
┌────────────────────────┬──────────────────────┬─────────────────────────────┐
│ Ephemeral PubKey (32)  │   Nonce (24 bytes)   │  Ciphertext + Auth Tag      │
└────────────────────────┴──────────────────────┴─────────────────────────────┘
```

### AES-256-GCM Format (V2 - Partial)

Intended for per-record encryption in V2 scheme.

```
┌────────────────────┬──────────────────────┬─────────────────────────────┐
│ Version (1 byte)   │   Nonce (12 bytes)   │  Ciphertext + Auth Tag      │
│      0x00          │                      │                             │
└────────────────────┴──────────────────────┴─────────────────────────────┘
```

## CLI Configuration

### Stored Files

```
~/.happy/
├── master.key    # Base64-encoded masterSecret (32 bytes)
└── access.key    # JWT access token for API authentication
```

### Auth Request

The CLI sends `supportsV2: false` to request V1 format:

```go
reqBody := map[string]interface{}{
    "publicKey":  publicKeyB64,
    "supportsV2": false,  // Request V1 format (masterSecret)
}
```

## V2 Scheme Details (Unfinished)

The V2 scheme was designed for **per-record encryption keys** but is not fully implemented:

### Intended Architecture

```
Session/Machine Record
        │
        ▼
┌───────────────────────────────────────────────────────────────┐
│                 dataEncryptionKey (32 bytes)                   │
│            Random AES key, unique per record                   │
└───────────────────────────────────────────────────────────────┘
        │
        │ Box.encrypt(dataEncryptionKey, contentKeyPair.publicKey)
        ▼
┌───────────────────────────────────────────────────────────────┐
│              encryptedDataKey (stored in DB)                   │
│     [version=0x00][Box-encrypted dataEncryptionKey]           │
└───────────────────────────────────────────────────────────────┘
```

### What's Implemented (Mobile App)

1. **Key derivation chain**: `masterSecret` → `contentDataKey` → `contentKeyPair`
2. **Encryption infrastructure**: `AES256Encryption` class exists
3. **Key wrapping functions**: `encryptEncryptionKey()` / `decryptEncryptionKey()`
4. **Encryptor selection**: `openEncryption(dataKey)` returns AES or SecretBox

### What's NOT Implemented

1. **No per-record keys in practice**: Sessions/machines get `null` dataKey
2. **Falls back to legacy**: All encryption uses `legacyEncryption` (SecretBox + masterSecret)
3. **CLI doesn't support V2**: Would need to decrypt wrapped per-record keys

### Why V2 Exists But Isn't Used

The `supportsV2` flag and V2 response format were added anticipating future migration to per-record keys. However:

- The mobile app infrastructure exists but isn't activated
- All `dataKey` values are `null`, triggering legacy fallback
- Migration would require coordinating mobile app + CLI + server changes

## Cross-Platform Compatibility

### Libraries Used

| Platform | Library | Notes |
|----------|---------|-------|
| Mobile (React Native) | `libsodium-wrappers` | Via `@/encryption/libsodium` |
| Go CLI | `golang.org/x/crypto/nacl/secretbox` | Standard Go crypto |
| Go CLI | `golang.org/x/crypto/nacl/box` | For key exchange |

### Verified Compatible

- SecretBox encryption/decryption between Go and JavaScript
- Box encryption/decryption for authentication
- Base64 encoding (standard, not URL-safe for stored keys)

### Test Vectors

Cross-platform test vectors are available in:
- `internal/crypto/secretbox_test.go` - Go tests
- `sources/trash/test-secretbox-vectors.ts` - TypeScript generator

## Security Considerations

### Current Model (V1)

- **Single key per account**: All devices share `masterSecret`
- **Compromise impact**: If one device is compromised, all data is exposed
- **No forward secrecy**: Same key used for all historical messages

### Intended Model (V2)

- **Per-record keys**: Each session/machine has unique `dataEncryptionKey`
- **Key wrapping**: Per-record keys encrypted with `contentKeyPair`
- **Revocation possible**: Could revoke access to specific records
- **Not yet implemented**: Infrastructure exists but not activated

## Troubleshooting

### "Decryption failed" Errors

1. **Check key format**: Ensure CLI is using V1 (masterSecret), not V2
2. **Re-authenticate**: Delete `~/.happy/` and run `./happy auth` again
3. **Verify key match**: Compare first 8 bytes of stored key with expected

### Key Mismatch Symptoms

- `secretbox.Open` returns `false` (authentication failed)
- Decrypted JSON is garbage or empty
- Error: "message authentication failed"

### Debug Commands

```bash
# Check stored key (first 8 bytes)
head -c 32 ~/.happy/master.key | base64 -d | xxd | head -1

# Run CLI with debug logging
DEBUG=1 ./happy

# Run tests
go test ./internal/crypto/... -v
```

## References

- [NaCl Documentation](https://nacl.cr.yp.to/)
- [libsodium Documentation](https://doc.libsodium.org/)
- [Go crypto/nacl](https://pkg.go.dev/golang.org/x/crypto/nacl)
