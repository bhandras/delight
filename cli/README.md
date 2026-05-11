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
  --model=gpt-5.4 \
  --log-level=debug
```

## Configuration

The CLI loads `~/.delight/config.toml` when it exists. You can select another
file with `--config` and a profile with `--profile`. See
`sample-config.toml` for every supported key with default values.

```toml
server_url = "https://your.server:3443"
agent = "codex"
mode = "remote"
log_level = "info"

[push]
mode = "auto"
events = ["turn-complete", "attention"]
cooldown_sec = 60

[codex]
model = "gpt-5.5"
reasoning_effort = "high"
permission_mode = "default"
extra_args = ["-c", "experimental=true"]

[profiles.local]
server_url = "http://localhost:3005"
```

Precedence is built-in defaults, TOML config, environment variables, then CLI
flags. Raw backend flags can also be passed after `--`:

```bash
./delight codex run --profile local -- -c experimental=true
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
- `docs/PUSH_NOTIFICATIONS.md` - Push architecture and CLI push env controls
