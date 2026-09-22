# mini-opencode build entry point.
#
# The version reported by the binary is derived from git at build time and
# injected with -ldflags, so a build always identifies the exact revision it
# came from without anyone editing a version constant.
#
#   tagged commit        -> 0.4.0
#   commits past tag     -> 0.4.0-dev.3.gabc1234   (3 commits, short sha)
#   no tags at all       -> 0.4.0-dev.gabc1234
#   dirty working tree   -> ...-dirty
#
# The fallback when nothing is injected (a plain `go build ./...`) is the
# version constant in internal/app/app.go.

SHELL := /bin/bash
BINARY := mini-opencode
PKG := ./cmd/mini-opencode
VERSION_PKG := github.com/wislist/mini-opencode/internal/app

# GOFLAGS/GOCACHE defaults: the repo keeps a local build cache because the
# sandbox cannot always write the shared one.
export GOCACHE ?= $(CURDIR)/.gocache
export GOFLAGS ?= -mod=mod
export GOMODCACHE ?= $(CURDIR)/.gocache/mod
export GOPATH ?= $(CURDIR)/.gocache/gopath

# VERSION can be overridden: `make build VERSION=1.0.0`
VERSION ?= $(shell ./scripts/version.sh)

LDFLAGS := -X main.version=$(VERSION)

.PHONY: help
help: ## Show available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the binary into ./bin (version from git)
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(PKG)
	@echo "built bin/$(BINARY) $(VERSION)"

.PHONY: install
install: ## Install into GOPATH/bin (version from git)
	go install -ldflags "$(LDFLAGS)" $(PKG)
	@echo "installed $(BINARY) $(VERSION)"

.PHONY: run
run: ## Run the TUI (version from git)
	go run -ldflags "$(LDFLAGS)" $(PKG)

.PHONY: test
test: ## Run all tests
	go test ./...

.PHONY: race
race: ## Run all tests with the race detector
	go test -race ./...

.PHONY: cover
cover: ## Run tests with a coverage summary
	go test -cover ./...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: fmt
fmt: ## Format all Go source
	gofmt -w internal cmd

.PHONY: fmt-check
fmt-check: ## Fail if any file is not gofmt-clean
	@out=$$(gofmt -l internal cmd); \
	if [ -n "$$out" ]; then echo "not gofmt-clean:"; echo "$$out"; exit 1; fi

.PHONY: check
check: fmt-check vet test ## Format check, vet, and test

.PHONY: clean
clean: ## Remove build output
	rm -rf bin

.PHONY: version
version: ## Print the version this build would report
	@echo $(VERSION)

.PHONY: tag
tag: ## Tag the current commit as v<base version> (refuses a dirty tree)
	@if ! git diff --quiet || ! git diff --cached --quiet; then \
		echo "refusing to tag: the working tree has uncommitted changes"; exit 1; \
	fi; \
	base=$$(echo $(VERSION) | sed 's/[-+].*$$//'); \
	if git rev-parse -q --verify "refs/tags/v$$base" >/dev/null; then \
		echo "tag v$$base already exists"; exit 1; \
	fi; \
	echo "tagging v$$base"; \
	git tag -a "v$$base" -m "v$$base"
