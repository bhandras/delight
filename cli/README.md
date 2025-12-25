# Delight CLI (Go)

Minimal CLI wrapper for Claude Code with mobile remote control - reimplemented in Go.

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

# Configure
export DELIGHT_SERVER_URL=http://localhost:3005

# Run
./delight
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
delight              # Start Claude Code session
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
