.PHONY: build fmt-check race test test-migrations-postgres vet verify verify-all

GOCACHE ?= /tmp/akv-go-cache
export GOCACHE

fmt-check:
	@test -z "$$(gofmt -l $$(find cmd internal -name '*.go' -type f))"

test:
	go test ./...

race:
	go test -race ./...

test-migrations-postgres:
	./scripts/test-migrations-postgres.sh

vet:
	go vet ./...

verify: fmt-check vet test

verify-all: verify race test-migrations-postgres

build:
	mkdir -p bin
	go build -o bin/akv-control ./cmd/akv-control
	go build -o bin/akv-execution-proxy ./cmd/akv-execution-proxy
	go build -o bin/akv-worker ./cmd/akv-worker
	go build -o bin/akv-mcp-server ./cmd/akv-mcp-server
	go build -o bin/akv-bootstrap-admin ./cmd/akv-bootstrap-admin
