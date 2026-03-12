VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

BINARY     := kube-less
CMD        := ./cmd/kube-less
BIN_DIR    := ./bin
DIST_DIR   := ./dist

LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)

.PHONY: all build test vet lint fmt clean dist docs help

all: vet test build

## build: compile kube-less binary to ./bin/kube-less
build:
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD)

## test: run all tests with race detector
test:
	go test ./... -timeout 60s -count=1 -race

## vet: run go vet
vet:
	go vet ./...

## lint: run golangci-lint
lint:
	golangci-lint run ./...

## fmt: format all Go source files
fmt:
	gofmt -w .

## clean: remove build artifacts
clean:
	rm -rf $(BIN_DIR) $(DIST_DIR)

## dist: cross-compile binaries for linux/amd64 and linux/arm64
dist:
	@mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY)-linux-amd64 $(CMD)
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY)-linux-arm64 $(CMD)
	@echo "Built:"
	@ls -lh $(DIST_DIR)/

## docs: generate docs/GODOC.md from go doc (run before each release)
docs:
	@./scripts/prerelease.sh

## help: show this help message
help:
	@echo "Usage: make <target>"
	@echo ""
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /' | column -t -s ':'
