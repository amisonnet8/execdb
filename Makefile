BIN         := bin/execdb
PKG         := ./cmd/execdb
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_FLAGS := -trimpath
LDFLAGS     := -s -w -X main.version=$(VERSION)

.DEFAULT_GOAL := help
.PHONY: help build run unit e2e test fmt fmt-check vet lint check check-deps tidy clean

help: ## Show available targets
	@grep -hE '^[a-zA-Z0-9_-]+:.*##' $(MAKEFILE_LIST) | sed 's/:.*##/\t/'

build: ## Build bin/execdb
	go build $(BUILD_FLAGS) -ldflags '$(LDFLAGS)' -o $(BIN) $(PKG)

run: build ## Build and start the REPL
	./$(BIN)

unit: ## Run Go unit tests
	go test ./...

e2e: build ## Run end-to-end checks in examples/
	bash examples/e2e.sh

test: unit e2e ## unit + e2e (this is what .claude/rules/testing.md refers to)

fmt: ## Format sources in place
	gofmt -s -w .

fmt-check: ## Fail if sources are unformatted (for CI)
	@unformatted="$$(gofmt -s -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "$$unformatted" 1>&2; \
		exit 1; \
	fi

vet: ## go vet
	go vet ./...

lint: ## staticcheck (skipped if not installed)
	@command -v staticcheck >/dev/null && staticcheck ./... || echo "staticcheck not installed; skipped"

check-deps: ## Enforce spec §6: engine must not import net directly
	@if go list -f '{{join .Imports "\n"}}' ./engine | grep -qx 'net\|net/http'; then \
		echo "ERROR: engine must not depend on net (see execdb_spec.md §6)"; exit 1; fi

tidy: ## go mod tidy and fail if it changed anything
	go mod tidy
	git diff --exit-code go.mod go.sum

check: fmt-check vet check-deps unit ## Fast pre-commit gate (also used by CI)

clean: ## Remove build artifacts and ExecDB leftovers
	rm -rf bin dist
	rm -f *.execdb_old
