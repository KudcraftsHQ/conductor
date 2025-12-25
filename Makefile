.PHONY: build build-all install install-global clean dev test fmt lint

# Binary name
BINARY_NAME=conductor

# Build directory
BUILD_DIR=build

# Version (can be overridden)
VERSION ?= 0.12.25.7

# Build flags
LDFLAGS=-ldflags "-X main.version=$(VERSION)"

# Default target
all: build

# Build for current platform
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/conductor

# Build for all platforms
build-all: clean
	@echo "Building for all platforms..."
	@mkdir -p $(BUILD_DIR)

	# Linux
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/conductor
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/conductor

	# macOS
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/conductor
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/conductor

	# Windows
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/conductor

	@echo "Build complete!"
	@ls -la $(BUILD_DIR)

# Install to user directory (recommended - supports auto-updates)
install: build
	@echo "Installing $(BINARY_NAME) to ~/.local/bin..."
	@mkdir -p ~/.local/bin
	@cp $(BUILD_DIR)/$(BINARY_NAME) ~/.local/bin/$(BINARY_NAME)
	@chmod +x ~/.local/bin/$(BINARY_NAME)
	@echo "✓ Installed to ~/.local/bin/$(BINARY_NAME)"
	@echo ""
	@echo "Make sure ~/.local/bin is in your PATH:"
	@echo "  export PATH=\"\$$HOME/.local/bin:\$$PATH\""
	@echo ""
	@echo "Add this to your ~/.zshrc or ~/.bashrc to make it permanent."

# Install globally to /usr/local/bin (requires sudo, disables auto-updates)
install-global: build
	@echo "Installing $(BINARY_NAME) to /usr/local/bin..."
	sudo cp $(BUILD_DIR)/$(BINARY_NAME) /usr/local/bin/$(BINARY_NAME)
	@echo "✓ Installed to /usr/local/bin/$(BINARY_NAME)"
	@echo ""
	@echo "⚠ Note: Auto-updates are disabled for system installations."
	@echo "To enable auto-updates, use 'make install' instead."

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -rf $(BUILD_DIR)
	go clean

# Development: build and run
dev: build
	./$(BUILD_DIR)/$(BINARY_NAME)

# Run tests
test:
	go test -v ./...

# Format code
fmt:
	go fmt ./...

# Lint code
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed. Run: brew install golangci-lint"; \
	fi

# Get dependencies
deps:
	go mod download
	go mod tidy

# Show help
help:
	@echo "Available targets:"
	@echo "  build          - Build for current platform"
	@echo "  build-all      - Build for all platforms"
	@echo "  install        - Install to ~/.local/bin (recommended, supports auto-updates)"
	@echo "  install-global - Install to /usr/local/bin (requires sudo, disables auto-updates)"
	@echo "  clean          - Remove build artifacts"
	@echo "  dev            - Build and run"
	@echo "  test           - Run tests"
	@echo "  fmt            - Format code"
	@echo "  lint           - Lint code"
	@echo "  deps           - Download dependencies"
