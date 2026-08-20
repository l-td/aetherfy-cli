# Aetherfy CLI Makefile

# Variables
BINARY_NAME=afy
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE?=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS=-ldflags "-s -w -X github.com/l-td/aetherfy-cli/pkg/version.Version=$(VERSION) -X github.com/l-td/aetherfy-cli/pkg/version.Commit=$(COMMIT) -X github.com/l-td/aetherfy-cli/pkg/version.BuildDate=$(BUILD_DATE)"

# GOPATH is only sometimes exported. `go env GOPATH` always answers
# (~/go when unset), and `?=` keeps an explicit environment GOPATH winning.
# Without this, install/uninstall expanded to /bin/afy: the install wrote
# outside the user's tree and the uninstall deleted from it.
GOPATH ?= $(shell go env GOPATH)

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOMOD=$(GOCMD) mod
GOVET=$(GOCMD) vet
GOFMT=gofmt

# Build directories
BUILD_DIR=./build
DIST_DIR=./dist

.PHONY: all build clean test lint fmt help install uninstall release cp-error-snapshot

## Default target
all: clean lint test build

## Build the binary
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) .
	@echo "Built: $(BUILD_DIR)/$(BINARY_NAME)"

## Build for current platform with debug symbols
build-debug:
	@echo "Building $(BINARY_NAME) with debug symbols..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -gcflags="all=-N -l" -o $(BUILD_DIR)/$(BINARY_NAME) .

## Install to GOPATH/bin
install: build
	@echo "Installing $(BINARY_NAME)..."
	@mkdir -p "$(GOPATH)/bin"
	@cp "$(BUILD_DIR)/$(BINARY_NAME)" "$(GOPATH)/bin/$(BINARY_NAME)"
	@echo "Installed to $(GOPATH)/bin/$(BINARY_NAME)"

## Uninstall from GOPATH/bin
uninstall:
	@echo "Uninstalling $(BINARY_NAME)..."
	@rm -f "$(GOPATH)/bin/$(BINARY_NAME)"

## Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v -race -cover ./...

## Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	@mkdir -p $(BUILD_DIR)
	$(GOTEST) -v -race -coverprofile=$(BUILD_DIR)/coverage.out ./...
	$(GOCMD) tool cover -html=$(BUILD_DIR)/coverage.out -o $(BUILD_DIR)/coverage.html
	@echo "Coverage report: $(BUILD_DIR)/coverage.html"

## Regenerate the control-plane error-code snapshot (needs ../aetherfy-control-plane)
cp-error-snapshot:
	@echo "Regenerating test/cp-error-codes-snapshot.json..."
	$(GOCMD) run ./scripts/cp-error-codes-snapshot

## Run linting
lint:
	@echo "Running linter..."
	$(GOVET) ./...
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed, skipping..."; \
	fi

## Format code
fmt:
	@echo "Formatting code..."
	$(GOFMT) -s -w .

## Check formatting
fmt-check:
	@echo "Checking code formatting..."
	@diff=$$($(GOFMT) -s -d .); \
	if [ -n "$$diff" ]; then \
		echo "$$diff"; \
		exit 1; \
	fi

## Download dependencies
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy

## Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR) $(DIST_DIR)
	@$(GOCMD) clean

## Build for all platforms
build-all: clean
	@echo "Building for all platforms..."
	@mkdir -p $(DIST_DIR)

	@# Linux
	GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-linux-amd64 .
	GOOS=linux GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-linux-arm64 .

	@# macOS
	GOOS=darwin GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-darwin-amd64 .
	GOOS=darwin GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-darwin-arm64 .

	@# Windows
	GOOS=windows GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(DIST_DIR)/$(BINARY_NAME)-windows-amd64.exe .

	@echo "Built all platforms in $(DIST_DIR)/"
	@ls -la $(DIST_DIR)/

## Create release archives
release: build-all
	@echo "Creating release archives..."
	@cd $(DIST_DIR) && \
		for f in $(BINARY_NAME)-*; do \
			if [[ "$$f" == *.exe ]]; then \
				zip "$${f%.exe}.zip" "$$f"; \
			else \
				tar czf "$$f.tar.gz" "$$f"; \
			fi; \
		done
	@echo "Release archives created in $(DIST_DIR)/"

## Run the CLI
run: build
	@$(BUILD_DIR)/$(BINARY_NAME) $(ARGS)

## Show version
version:
	@echo "Version: $(VERSION)"
	@echo "Commit:  $(COMMIT)"
	@echo "Date:    $(BUILD_DATE)"

## Show help
help:
	@echo "Aetherfy CLI - Makefile Commands"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@awk '/^[a-zA-Z_-]+:.*?## / { \
		helpMessage = match($$0, /## (.*)/); \
		if (helpMessage) { \
			target = $$1; \
			sub(/:.*/, "", target); \
			printf "  %-15s %s\n", target, substr($$0, RSTART + 3, RLENGTH); \
		} \
	}' $(MAKEFILE_LIST)
	@echo ""
	@echo "Examples:"
	@echo "  make build          # Build binary"
	@echo "  make test           # Run tests"
	@echo "  make install        # Install to GOPATH/bin"
	@echo "  make build-all      # Build for all platforms"
	@echo "  make release        # Create release archives"
