.PHONY: build clean install test lint fmt check-fmt run help vuln gen-docs check-docs

# Variables
BINARY_NAME=truffle
VERSION?=0.1.0
BUILD_DIR=bin
MAIN_PATH=.

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod

# Build flags
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -s -w"

# Default target
all: test build

## gen-docs: Regenerate the committed command/flag reference (docs-gen/) from the CLI.
gen-docs:
	$(GOCMD) run . gen-docs --out docs-gen

## check-docs: Drift gate — regenerate and fail if the committed reference is stale.
check-docs: gen-docs
	git diff --exit-code docs-gen/ || { echo "::error::docs-gen/ is stale — run 'make gen-docs' and commit"; exit 1; }

## build: Build the binary
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

## build-all: Build for multiple platforms
build-all:
	@echo "Building for multiple platforms..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 $(MAIN_PATH)
	GOOS=linux GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 $(MAIN_PATH)
	GOOS=darwin GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 $(MAIN_PATH)
	GOOS=darwin GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 $(MAIN_PATH)
	GOOS=windows GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe $(MAIN_PATH)
	@echo "Multi-platform build complete"

## install: Install the binary to system
install: build
	@echo "Installing $(BINARY_NAME)..."
	@sudo cp $(BUILD_DIR)/$(BINARY_NAME) /usr/local/bin/
	@echo "Installation complete"

## clean: Remove build artifacts
clean:
	@echo "Cleaning..."
	@$(GOCLEAN)
	@rm -rf $(BUILD_DIR)
	@echo "Clean complete"

## test: Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v ./...

## test-coverage: Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	$(GOTEST) -v -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

## vuln: Run govulncheck
vuln:
	@echo "Running govulncheck..."
	@govulncheck ./...
	@echo "✓ No known vulnerabilities"

## lint: Run linter
lint:
	@echo "Running linter..."
	@which golangci-lint > /dev/null || (echo "golangci-lint not installed. Run: curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$(go env GOPATH)/bin" && exit 1)
	golangci-lint run

## fmt: Format code (rewrites files)
fmt:
	@echo "Formatting code..."
	@$(GOCMD) fmt ./...
	@echo "Format complete"

## check-fmt: Formatting gate — REPORT drift, don't fix it. Run in CI.
#
# Distinct from `fmt`, which rewrites files and always exits 0. That is
# convenient locally but it cannot fail a build, so it never gated anything:
# 7 files sat unformatted on main and surfaced as unrelated diffs in whatever
# PR ran the formatter next (#122).
#
# Excludes vendor/ and lists offenders with a diff, so the fix is obvious.
# gofmt walks paths rather than modules, so this also covers any submodule.
check-fmt:
	@files=$$(gofmt -l . 2>/dev/null | grep -v '^vendor/' || true); \
	if [ -n "$$files" ]; then \
	  echo "::error::these files are not gofmt-clean — run 'gofmt -w' on them:"; \
	  echo "$$files" | sed 's/^/  /'; \
	  echo; gofmt -d $$files; \
	  exit 1; \
	fi; \
	echo "✓ gofmt clean"

## deps: Download dependencies
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy
	@echo "Dependencies downloaded"

## run: Run the application
run: build
	@$(BUILD_DIR)/$(BINARY_NAME)

## run-search: Run a sample search
run-search: build
	@$(BUILD_DIR)/$(BINARY_NAME) search "m5.large" --verbose

## help: Show this help message
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'

# Development helpers
.PHONY: dev
dev: fmt lint test build

.PHONY: release
release: clean test build-all
	@echo "Release build complete"
