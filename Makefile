GO        ?= go
PLATFORMS ?= darwin/arm64,darwin/amd64

.DEFAULT_GOAL := build

.PHONY: dev build fmt vet test race check clean

dev: ## Run the app.
	$(GO) tool mygo dev

build: ## Build the app for both darwin architectures into build/.
	$(GO) tool mygo build -platform "$(PLATFORMS)"

fmt: ## Format Go sources.
	gofmt -w main.go internal

vet: ## Vet Go code and the MyGo configuration.
	$(GO) vet ./...
	$(GO) tool mygo vet .

test: ## Run unit and Native UI tests.
	CGO_ENABLED=0 $(GO) test ./... -count=1

race: ## Run race tests over the stateful packages.
	$(GO) test -race ./internal/app/... ./internal/runtime/... ./internal/storage/... ./internal/logging/... ./internal/ui/...

check: ## Fail on unformatted code, then vet, test and race.
	@unformatted="$$(gofmt -l main.go internal)"; \
	if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi
	$(GO) vet ./...
	$(GO) tool mygo vet .
	CGO_ENABLED=0 $(GO) test ./... -count=1
	$(GO) test -race ./internal/app/... ./internal/runtime/... ./internal/storage/... ./internal/logging/... ./internal/ui/...

clean: ## Remove build output.
	rm -rf build