# Delight Server (Go)

Private, self-hosted backend for the Delight iOS app. It stores metadata,
coordinates pairing/auth, and fans out updates over WebSockets while keeping
message payloads end-to-end encrypted.

This server is experimental and intended primarily for personal use.

## Why This Exists

The original Delight server is abandoned and closed-source hosted. This is a
clean-room implementation in Go that:

- ✅ **100% Privacy** - You control the server, all data is end-to-end encrypted
- ✅ **Single Binary Core** - Go + SQLite server, optional gorush sidecar
- ✅ **Minimal & Auditable** - Core features only, easy to verify security
- ✅ **iOS App Compatible** - Works with the existing Delight iOS app

## Features

- 🔐 **Challenge-Response Authentication** - Ed25519 signature-based auth
- 🔑 **QR Code Auth** - Secure CLI → Mobile pairing via X25519 encryption
- 💬 **Real-time Sync** - WebSocket-based session and message synchronization
- 🔒 **End-to-End Encryption** - Box handshake + AES-256-GCM session payloads
- 🔔 **Encrypted Push Relay** - Forwards ciphertext to gorush/APNs
- 📱 **Multi-device** - Seamless sync between CLI, mobile, and daemon
- 🚀 **Self-hosted** - Single binary + SQLite database

## Architecture

- **Web Framework**: Gin
- **Database**: SQLite with sqlc for type-safe queries
- **WebSocket**: Socket.IO (via go-socket.io)
- **Encryption**:
  - AES-256-GCM - Per-session keys
  - TweetNaCl Box (X25519) - Auth handshake

## Quick Start

```bash
# Generate master secret
make secret

# Create .env file
cat > .env <<EOF
PORT=3005
DELIGHT_MASTER_SECRET=<paste-secret-from-above>
DATABASE_PATH=./delight.db
DEBUG=true
EOF

# Build and run
make run

# Or just run without building
go run ./cmd/server
```

**Server will start on:** `http://localhost:3005`

## Public Hosting (HTTPS with Caddy)

For public hosting, the easiest setup is to run the Go server behind Caddy.
Caddy automatically provisions and renews Let's Encrypt certificates.

Prereqs:

- A DNS hostname pointing to your server (e.g. `api.example.com`)
- TCP port `443` reachable from the public internet
  - Note: this deployment is HTTPS-only and does not bind host port `80`.

Start:

```bash
./deploy/delight-server.sh init
${EDITOR:-vi} ./deploy-data/.env
./deploy/delight-server.sh up
```

If you already run Caddy on the host and want to disable the bundled Caddy
container, use:

```bash
./deploy/delight-server.sh up --no-caddy
```

When Caddy is disabled, the compose override publishes `3005` so your host
proxy can forward traffic to `http://localhost:3005`.

Data on the host filesystem:

- Server DB: `./deploy-data/server/delight.db`
- Server logs: `./deploy-data/server/server.log`
- Caddy access log: `./deploy-data/caddy/data/access.log`
- Caddy TLS state/certs: `./deploy-data/caddy/`
- gorush config/key: `./deploy-data/gorush/`

## HTTPS (Optional)

By default the server runs over plain HTTP. You can enable HTTPS directly in
the Go server by providing a certificate and key:

```bash
go run ./cmd/server \
  --tls \
  --tls-cert-file=./tls/cert.pem \
  --tls-key-file=./tls/key.pem
```

When HTTPS is enabled, configure the iOS app server URL as:
`https://your-server:3005`.

## Configuration

Environment variables:

- `PORT` - HTTP server port (default: 3005)
- `DELIGHT_MASTER_SECRET` - Master secret for token signing (required)
- `DATABASE_PATH` - SQLite database path (default: ./delight.db)
- `DELIGHT_PUSH_BACKEND` - Push backend (optional, `gorush` supported)
- `DELIGHT_GORUSH_URL` - gorush endpoint (default: `http://gorush:8088/api/push`)
- `DELIGHT_PUSH_TOPIC` - APNs topic / iOS bundle id (required when push enabled)

Command-line flags (override env defaults):

- `--addr` - Listen address (default `:3005` or `$PORT`)
- `--db-path` - SQLite database path
- `--master-secret` - Master secret for JWT signing (required)
- `--debug` - Enable debug logging
- `--log-file` - Optional log file path (also logs to stderr)
- `--tls` - Enable HTTPS
- `--tls-cert-file` - TLS certificate PEM file (required with `--tls`)
- `--tls-key-file` - TLS private key PEM file (required with `--tls`)

## iOS App Setup

1. Start the server: `./server`
2. In the Delight iOS app settings, change server URL to: `http://your-server:3005`
3. That's it! The app will work with your private server.

## Project Structure

```
cmd/server/         - Main entry point
internal/
  api/              - HTTP handlers and routing
  websocket/        - Socket.IO WebSocket server
  crypto/           - Encryption implementations
  database/         - SQLite schema and queries
  models/           - Generated sqlc models
  config/           - Configuration management
```

## Security Notes

- All sensitive data (messages, metadata) is end-to-end encrypted
- The server cannot decrypt your conversations
- Authentication uses Ed25519 signatures (no passwords)
- Session keys never leave your devices unencrypted
- Master secret is used only for JWT signing
- Push payloads are encrypted by the CLI and decrypted on device

## Development

```bash
# Install dependencies
go mod download

# Generate sqlc code
sqlc generate

# Run tests
go test ./...

# Run with hot reload (requires air)
air
```

## License

MIT - Use freely, modify as needed, self-host anywhere.

## Credits

This server is part of the Delight stack and implements the Delight protocol.
