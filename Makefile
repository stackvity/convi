# Makefile for convi local development

SHELL := /bin/bash
GO_VERSION ?= 1.21 # Minimum Go version required
GOLANGCILINT_VERSION ?= latest # Or pin to a specific version, e.g., v1.59.1

# --- Go Commands ---
GO := go
GOFMT := $(GO) fmt ./...
GOIMPORTS := goimports -w .
GOTEST := $(GO) test -race -cover ./...
GOVET := $(GO) vet ./...
# Build command: Include ldflags for version embedding
VERSION ?= $(shell git describe --tags --always --dirty || echo "dev")
COMMIT ?= $(shell git rev-parse --short HEAD)
BUILD_DATE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
LDFLAGS_VARS = -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(BUILD_DATE)
GOBUILD_FLAGS := -ldflags="-s -w $(LDFLAGS_VARS)"
GOBUILD := $(GO) build $(GOBUILD_FLAGS) -o convi .
GORUN := $(GO) run .
GOMODTIDY := $(GO) mod tidy
GOMODVERIFY := $(GO) mod verify
GOVULNCHECK := govulncheck ./...

# --- Lint Command ---
GOLANGCILINT := golangci-lint
LINT := $(GOLANGCILINT) run ./... --config .golangci.yml

# --- Checksum Command (Portable) ---
# Assumes a simple Go helper program exists at ./cmd/checksum/main.go
# Usage: go run ./cmd/checksum <input_file> <output_checksum_file>
# This provides a cross-platform way to generate SHA256 checksums.
CHECKSUM_CMD := $(GO) run ./cmd/checksum/main.go

# --- Phony Targets ---
.PHONY: all install-deps lint test build run clean help check-deps format vuln-check \
	build-all build-linux-amd64 build-darwin-amd64 build-darwin-arm64 build-windows-amd64 checksum-helper-check

# --- Default Target ---
all: build

# --- Dependency Checks ---
check-deps:
	@echo "==== Checking dependencies ===="
	# Go Installation Check
	@if ! command -v $(GO) &> /dev/null; then \
		echo "Error: go is not installed. Please install Go $(GO_VERSION) or later." >&2; \
		exit 1; \
	fi
	# Go Version Check
	@go_version=$$($(GO) version | grep -oP 'go\K[0-9]+\.[0-9]+(\.[0-9]+)?'); \
	min_version=$(GO_VERSION); \
	if ! printf '%s\n' "$$min_version" "$$go_version" | sort -V -C; then \
		echo "Error: Go version $$go_version is older than required minimum $(GO_VERSION)." >&2; \
		exit 1; \
	fi
	# Development Tool Checks
	@if ! command -v $(GOLANGCILINT) &> /dev/null; then \
		echo "Warning: $(GOLANGCILINT) not found. Run 'make install-deps' to install." >&2; \
	fi
	@if ! command -v goimports &> /dev/null; then \
		echo "Warning: goimports not found. Run 'make install-deps' to install." >&2; \
	fi
	@if ! command -v govulncheck &> /dev/null; then \
		echo "Warning: govulncheck not found. Run 'make install-deps' to install." >&2; \
	fi
	@echo "==== Dependencies OK ===="

# --- Install Development Dependencies ---
install-deps:
	@echo "==== Installing development dependencies ===="
	$(GO) install golang.org/x/tools/cmd/goimports@latest
	$(GO) install golang.org/x/vuln/cmd/govulncheck@latest
	# Install golangci-lint
	@if ! command -v $(GOLANGCILINT) &> /dev/null; then \
		echo "Installing $(GOLANGCILINT)..."; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$(go env GOPATH)/bin $(GOLANGCILINT_VERSION); \
	else \
		echo "$(GOLANGCILINT) already installed."; \
	fi
	@echo "==== Dependencies installed (ensure $$(go env GOPATH)/bin is in your PATH) ===="

# --- Code Quality & Testing ---
format:
	@echo "==== Formatting code ===="
	$(GOFMT)
	$(GOIMPORTS)

lint: check-deps
	@echo "==== Running linters ===="
	$(LINT) || { echo "Error: Linting failed." >&2; exit 1; }

test: check-deps
	@echo "==== Running tests ===="
	$(GOMODVERIFY) # Verify module integrity before testing
	$(GOTEST) || { echo "Error: Tests failed." >&2; exit 1; }

vuln-check: check-deps
	@echo "==== Running vulnerability check ===="
	$(GOVULNCHECK) || { echo "Warning: govulncheck reported issues." >&2; } # Don't fail build by default

# --- Build Targets ---
build: check-deps format lint test vuln-check
	@echo "==== Building convi binary (version: $(VERSION), commit: $(COMMIT), date: $(BUILD_DATE)) ===="
	$(GOMODTIDY)
	$(GOBUILD) || { echo "Error: Build failed." >&2; exit 1; }
	@echo "==== Build complete: ./convi ===="

run: build
	@echo "==== Running convi (pass args via ARGS=...) ===="
	./convi $(ARGS)

clean:
	@echo "==== Cleaning build artifacts ===="
	@rm -f convi convi-*-* convi.exe convi-*.exe *.sha256 # Clean binaries & checksums
	@rm -f coverage.out
	@$(GO) clean -cache -testcache

# --- Cross-Compilation Targets ---
# Check for checksum helper before attempting cross-builds
checksum-helper-check:
	@if [ ! -f ./cmd/checksum/main.go ]; then \
		echo "Error: Checksum helper ./cmd/checksum/main.go not found. Cannot generate checksums." >&2; \
		exit 1; \
	fi

build-linux-amd64: check-deps checksum-helper-check
	@echo "==== Building for Linux AMD64 ===="
	GOOS=linux GOARCH=amd64 $(GO) build $(GOBUILD_FLAGS) -o convi-linux-amd64 .
	$(CHECKSUM_CMD) convi-linux-amd64 convi-linux-amd64.sha256

build-darwin-amd64: check-deps checksum-helper-check
	@echo "==== Building for Darwin (macOS) AMD64 ===="
	GOOS=darwin GOARCH=amd64 $(GO) build $(GOBUILD_FLAGS) -o convi-darwin-amd64 .
	$(CHECKSUM_CMD) convi-darwin-amd64 convi-darwin-amd64.sha256

build-darwin-arm64: check-deps checksum-helper-check
	@echo "==== Building for Darwin (macOS) ARM64 ===="
	GOOS=darwin GOARCH=arm64 $(GO) build $(GOBUILD_FLAGS) -o convi-darwin-arm64 .
	$(CHECKSUM_CMD) convi-darwin-arm64 convi-darwin-arm64.sha256

build-windows-amd64: check-deps checksum-helper-check
	@echo "==== Building for Windows AMD64 ===="
	GOOS=windows GOARCH=amd64 $(GO) build $(GOBUILD_FLAGS) -o convi-windows-amd64.exe .
	$(CHECKSUM_CMD) convi-windows-amd64.exe convi-windows-amd64.exe.sha256

# Target to build all release binaries
build-all: check-deps checksum-helper-check build-linux-amd64 build-darwin-amd64 build-darwin-arm64 build-windows-amd64
	@echo "==== All release binaries built ===="

# --- Help ---
help:
	@echo "Available commands for convi development:"
	@echo "  make all                   - Build the binary for the current platform (default)"
	@echo "  make install-deps         - Install necessary development tools (goimports, golangci-lint, govulncheck)"
	@echo "  make check-deps           - Check if required tools are installed"
	@echo "  make format               - Format Go source code"
	@echo "  make lint                 - Run linters"
	@echo "  make test                 - Run tests with race detector and coverage"
	@echo "  make vuln-check           - Run vulnerability check"
	@echo "  make build                - Build the convi executable for the current platform (Includes version embedding)"
	@echo "  make run ARGS=...         - Build and run convi, passing arguments via ARGS (e.g., make run ARGS='-i . -o out --verbose')"
	@echo "  make clean                - Remove build artifacts including cross-compiled binaries and checksums"
	@echo "  make build-all            - Build release binaries and checksums for Linux, macOS, Windows (amd64/arm64 where applicable)"
	@echo "  make build-linux-amd64    - Build for Linux AMD64 and generate checksum"
	@echo "  make build-darwin-amd64   - Build for macOS AMD64 and generate checksum"
	@echo "  make build-darwin-arm64   - Build for macOS ARM64 and generate checksum"
	@echo "  make build-windows-amd64  - Build for Windows AMD64 and generate checksum"
	@echo "  make checksum-helper-check - Verify the Go checksum helper exists (required for cross-builds)"
	@echo "  make help                 - Show this help message"
	@echo ""
	@echo "Variables:"
	@echo "  GO_VERSION              : $(GO_VERSION) (Minimum required Go version)"
	@echo "  GOLANGCILINT_VERSION    : $(GOLANGCILINT_VERSION) (Version of golangci-lint to install)"
	@echo "  ARGS                    : Arguments to pass to 'make run'"