.PHONY: build fmt-check race test test-migrations-postgres vet verify verify-all web-build web-deps web-test

GOCACHE ?= /tmp/akv-go-cache
export GOCACHE
WEB_DIR := internal/control/web
WEB_DEPS := $(WEB_DIR)/node_modules/.package-lock.json

$(WEB_DEPS): $(WEB_DIR)/package.json $(WEB_DIR)/package-lock.json $(WEB_DIR)/.npmrc
	npm --prefix $(WEB_DIR) ci

web-deps: $(WEB_DEPS)

web-build: web-deps
	npm --prefix $(WEB_DIR) run build

web-test: web-deps
	npm --prefix $(WEB_DIR) test

fmt-check:
	@test -z "$$(gofmt -l $$(find cmd internal -path '*/node_modules' -prune -o -name '*.go' -type f -print))"

test: web-build web-test
	go test ./...

race: web-build web-test
	go test -race ./...

test-migrations-postgres:
	./scripts/test-migrations-postgres.sh

vet: web-build
	go vet ./...

verify: fmt-check vet test

verify-all: verify race test-migrations-postgres

build: web-build
	mkdir -p bin
	go build -o bin/akv-control ./cmd/akv-control
	go build -o bin/akv-execution-proxy ./cmd/akv-execution-proxy
	go build -o bin/akv-worker ./cmd/akv-worker
	go build -o bin/akv-mcp-server ./cmd/akv-mcp-server
	go build -o bin/akv-bootstrap-admin ./cmd/akv-bootstrap-admin
