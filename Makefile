BINARY  := wrk
PKG     := ./cmd/wrk
BIN_DIR := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/BryanMcGahan/wrktree/internal/cli.Version=$(VERSION)

# Arguments passed to the binary by `make run`, e.g. make run ARGS="ls --json"
ARGS ?=

.DEFAULT_GOAL := build
.PHONY: build run install uninstall test test-race cover fmt vet lint tidy check clean help

build: ## Build bin/wrk
	go build -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY) $(PKG)

run: build ## Build and run (pass arguments with ARGS="...")
	./$(BIN_DIR)/$(BINARY) $(ARGS)

install: ## Install wrk into $GOBIN (or $GOPATH/bin)
	go install -ldflags '$(LDFLAGS)' $(PKG)

uninstall: ## Remove the installed wrk binary
	rm -f "$$(go env GOBIN)/$(BINARY)" "$$(go env GOPATH)/bin/$(BINARY)"

test: ## Run tests
	go test ./...

test-race: ## Run tests with the race detector
	go test -race ./...

cover: ## Run tests with a coverage report
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

fmt: ## Format code
	gofmt -w .

vet: ## Run go vet
	go vet ./...

tidy: ## Tidy go.mod / go.sum
	go mod tidy

check: ## Check formatting, vet and test (good before committing)
	@test -z "$$(gofmt -l .)" || (echo "unformatted files:"; gofmt -l .; exit 1)
	go vet ./...
	go test ./...

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) coverage.out

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'
