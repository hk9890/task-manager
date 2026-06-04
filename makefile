.PHONY: all build install test test-cli test-sdk vet fmt fmt-check lint clean tidy

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

PKG     := github.com/hk9890/agent-tasks/cmd
LDFLAGS := -ldflags "-X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE) -s -w"

INSTALL_BIN_DIR ?= $(if $(GOBIN),$(GOBIN),$(shell go env GOPATH)/bin)

all: build

# Build the atctl binary into ./bin.
build:
	@echo "Building atctl $(VERSION)..."
	@go build $(LDFLAGS) -o bin/atctl .

# Install atctl onto $PATH.
install:
	@echo "Installing atctl to $(INSTALL_BIN_DIR)..."
	@go build $(LDFLAGS) -o $(INSTALL_BIN_DIR)/atctl .

# Run all tests (both modules).
test: test-sdk test-cli

test-cli:
	@echo "Testing CLI module..."
	@go test ./...

test-sdk:
	@echo "Testing SDK module..."
	@cd sdk && go test ./...

vet:
	@go vet ./...
	@cd sdk && go vet ./...

fmt:
	@gofmt -w cmd sdk/tasks main.go

fmt-check:
	@out="$$(gofmt -l cmd sdk/tasks main.go)"; \
	if [ -n "$$out" ]; then echo "unformatted files:"; echo "$$out"; exit 1; fi

tidy:
	@go mod tidy
	@cd sdk && go mod tidy

clean:
	@rm -rf bin coverage.out
