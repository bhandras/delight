.PHONY: build run test clean lint

# Build the webclient bridge binary.
build:
	go build -o webclient .

# Build and run the webclient bridge.
run: build
	./webclient

# Run tests.
test:
	go test ./...

# Run linter.
lint:
	golangci-lint run ./...

# Clean local build output.
clean:
	rm -f webclient
