# Delight CLI (Go)

CLI for running agent sessions and syncing them to the Delight iOS app.

This CLI is experimental and intended primarily for personal use.

The CLI is responsible for:

- Running an agent backend (Codex, Claude, etc) in a terminal directory
- Pairing/auth with the server (for the mobile app to discover terminals)
- Encrypting message payloads end-to-end before they reach the server

## Quick Start

```bash
# Build
make build

# Show usage (default)
./delight

# Authenticate once (required before running sessions)
./delight auth --server-url=http://localhost:3005

# Run a session (Codex by default)
./delight run --server-url=http://localhost:3005

# Run a session with an explicit model + log level
./delight run \
  --server-url=http://localhost:3005 \
  --model=gpt-5.2-codex \
  --log-level=debug
```

## How It Works

```
Terminal directory
   ↓
Delight CLI runs an agent backend
   ↓
Encrypts messages end-to-end
   ↓
WebSocket → Delight server
   ↓
Relays updates to the Delight iOS app
```

## Commands

```bash
delight              # Show usage
delight run          # Start a session
delight claude run   # Start a session using Claude
delight codex run    # Start a session using Codex
delight acp run      # Start a session using ACP
delight auth         # Authenticate with server
delight version      # Show version
```

## Development

```bash
make build    # Build binary
make run      # Build and run
make test     # Run tests
make clean    # Clean build artifacts
```

## Related Docs

- `README.md` - Repo overview
- `server/README.md` - Server configuration and hosting
- `ios/README.md` - iOS app build and simulator workflow
