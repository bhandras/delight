# Delight CLI (Go)

Minimal CLI wrapper for agent sessions with mobile remote control.

## Features (Minimal MVP)

- ✅ Wrap Claude Code execution
- ✅ End-to-end encryption
- ✅ Real-time sync with delight-server-go
- ✅ Works with Delight iOS app
- ⏳ Daemon mode (coming soon)
- ⏳ RPC system (coming soon)

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
./delight run --server-url=http://localhost:3005 --model=gpt-5.2-codex --log-level=debug
```

## How It Works

```
Terminal
   ↓
delight-cli-go wraps `claude`
   ↓
Encrypts messages
   ↓
WebSocket → delight-server-go
   ↓
Relays to Delight iOS app
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

## Architecture

- **Single binary** - No dependencies
- **Compatible encryption** - Works with TypeScript version
- **Minimal surface** - Core features only (for now)
- **Fast startup** - <10ms

## Development

```bash
make build    # Build binary
make run      # Build and run
make test     # Run tests
make clean    # Clean build artifacts
```

## License

MIT
