.PHONY: build run test clean install

PKG := github.com/bhandras/delight/cli/internal/version

COMMIT_HASH := $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
LDFLAGS := -ldflags "-X $(PKG).CommitHash=$(COMMIT_HASH)"

# Build the binary
build:
	go build $(LDFLAGS) -o delight ./cmd/delight

# Run the CLI
run: build
	./delight

# Run tests
test:
	go test -v ./...

# Clean build artifacts
clean:
	rm -f delight

# Install to ~/go/bin
install: build
	cp delight ~/go/bin/

# Format code
fmt:
	go fmt ./...

# Run linter
lint:
	golangci-lint run

# Show help
help:
	@echo "Available commands:"
	@echo "  make build   - Build the delight cli binary"
	@echo "  make run     - Build and run"
	@echo "  make test    - Run tests"
	@echo "  make clean   - Remove build artifacts"
	@echo "  make install - Install to ~/go/bin"
