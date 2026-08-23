MODULE   := github.com/NathanBhanji/debrid-client
VERSION  ?= $(shell git describe --tags --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "")
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -s -w \
  -X $(MODULE)/internal/buildinfo.Version=$(VERSION) \
  -X $(MODULE)/internal/buildinfo.Commit=$(COMMIT) \
  -X $(MODULE)/internal/buildinfo.Date=$(DATE)
GOLANGCI_VERSION := $(shell cat .golangci-version)
GOLANGCI := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
SQLC     := go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1

.PHONY: build test lint vet tidy run clean generate generate-check

build: ## Build the binary into ./bin
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/debrid ./cmd/debrid

test: ## Run tests with race detector
	go test -race -count=1 ./...

vet:
	go vet ./...

lint: ## Run golangci-lint
	$(GOLANGCI) run ./...

tidy:
	go mod tidy
	@git diff --exit-code go.mod go.sum || (echo "go.mod/go.sum not tidy" && exit 1)

generate: ## Regenerate sqlc code
	cd internal/store && $(SQLC) generate

generate-check: generate ## Fail if generated code is stale or uncommitted
	@git add -N internal/store/sqlcgen && git diff --exit-code internal/store/sqlcgen || (echo "sqlc output is stale: run make generate and commit" && exit 1)

run: build
	./bin/debrid $(ARGS)

clean:
	rm -rf bin dist coverage.out
