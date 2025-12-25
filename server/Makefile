.PHONY: build run dev test clean migrate sqlc

# Build the server binary
build:
	go build -o server ./cmd/server

# Run the server
run: build
	( set -a; [ -f .env ] && . .env; set +a; ./server )

# Development mode with hot reload (requires air: go install github.com/cosmtrek/air@latest)
dev:
	air

# Run tests
test:
	go test -v ./...

# Generate sqlc code
sqlc:
	sqlc generate

# Clean build artifacts
clean:
	rm -f server
	rm -f delight.db delight.db-shm delight.db-wal

# Run migrations (manual - just copies schema)
migrate:
	@echo "SQLite migrations are applied automatically on startup"

# Install development tools
tools:
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
	go install github.com/cosmtrek/air@latest

# Format code
fmt:
	go fmt ./...

# Lint code (requires golangci-lint)
lint:
	golangci-lint run

# Generate random master secret
secret:
	@openssl rand -base64 32
