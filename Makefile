.PHONY: all build test test-race lint fmt clean ci

BIN_DIR := bin
BINARY := $(BIN_DIR)/nullspace
GOLANGCI_LINT := $(shell which golangci-lint 2>/dev/null || echo $(HOME)/go/bin/golangci-lint)
GCI := $(shell which gci 2>/dev/null || echo $(HOME)/go/bin/gci)

all: build

build:
	@mkdir -p $(BIN_DIR)
	go build -v -o $(BINARY) ./cmd/nullspace

test:
	go test -v ./...

test-race:
	go test -v -race ./...

lint:
	$(GOLANGCI_LINT) run ./cmd/... ./pkg/...

fmt:
	@gofmt -w cmd pkg
	@if [ -f "$(GCI)" ]; then \
		$(GCI) write --skip-generated -s standard -s default -s "prefix(github.com/carlpatchett/nullspace)" cmd pkg; \
	fi

clean:
	rm -rf $(BIN_DIR) tmp/

ci: fmt lint test-race build
	@echo "All CI quality gates passed successfully."
