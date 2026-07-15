.PHONY: fmt-check test test-migrations-postgres vet verify

GOCACHE ?= /tmp/akv-go-cache
export GOCACHE

fmt-check:
	@test -z "$$(gofmt -l $$(find cmd internal -name '*.go' -type f))"

test:
	go test ./...

test-migrations-postgres:
	./scripts/test-migrations-postgres.sh

vet:
	go vet ./...

verify: fmt-check vet test
