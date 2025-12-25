## Getting Started with Delight Server (Go)

### Prerequisites

- Go 1.21+ installed
- `make` command available
- OpenSSL (for generating master secret)

### Quick Start

1. **Clone and build**:
   ```bash
   cd delight-server-go
   make tools    # Install sqlc and air (optional)
   make build    # Build the server
   ```

2. **Generate master secret**:
   ```bash
   make secret
   # Output: random-base64-string-here
   ```

3. **Create `.env` file**:
   ```bash
   cp .env.example .env
   # Edit .env and set DELIGHT_MASTER_SECRET to the generated secret
   ```

4. **Run the server**:
   ```bash
   make run
   # Server starts on http://localhost:3005
   ```

5. **Configure Delight iOS app**:
   - Open Delight app settings
   - Change server URL to: `http://your-ip:3005`
   - Done! The app will now use your private server

### Development

```bash
# Run with hot reload
make dev

# Generate sqlc code after changing queries
make sqlc

# Run tests
make test

# Format code
make fmt
```

### Deploying

**Option 1: Single Binary**
```bash
make build
scp server your-server:/opt/delight/
ssh your-server
cd /opt/delight
DELIGHT_MASTER_SECRET="your-secret" ./server
```

**Option 2: systemd Service**
```bash
# Create /etc/systemd/system/delight-server.service
[Unit]
Description=Delight Server
After=network.target

[Service]
Type=simple
User=delight
WorkingDirectory=/opt/delight
Environment=DELIGHT_MASTER_SECRET=your-secret-here
Environment=PORT=3005
Environment=DATABASE_PATH=/var/lib/delight/delight.db
ExecStart=/opt/delight/server
Restart=always

[Install]
WantedBy=multi-user.target
```

**Option 3: Docker** (TODO)

### What Works Right Now

✅ **Authentication**:
- Challenge-response authentication (mobile app can create accounts)
- QR code authentication (CLI can pair with mobile)
- JWT token generation and verification

✅ **Database**:
- SQLite with all tables created
- Automatic migrations on startup

⏳ **Coming Soon**:
- Session management (create, list, sync)
- WebSocket real-time sync
- Machine registration

### Configuration Options

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `3005` | HTTP server port |
| `DELIGHT_MASTER_SECRET` | *required* | Master secret for JWT signing |
| `DATABASE_PATH` | `./delight.db` | SQLite database path |
| `DEBUG` | `false` | Enable debug logging |

### Troubleshooting

**"missing DELIGHT_MASTER_SECRET"**:
- Run `make secret` to generate one
- Set it in `.env` file or export as environment variable

**"failed to open database"**:
- Check that the directory for DATABASE_PATH exists
- Ensure write permissions on the database file

**iOS app can't connect**:
- Make sure the server is accessible from your phone
- Check firewall settings
- Use your local IP address, not `localhost`
- iOS requires HTTPS for production (use a reverse proxy like Caddy)

### Next Steps

See `PROGRESS.md` for implementation status and roadmap.
