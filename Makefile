.PHONY: fmt-check test vet verify

GOCACHE ?= /tmp/akv-go-cache
export GOCACHE

fmt-check:
	@test -z "$$(gofmt -l $$(find cmd internal -name '*.go' -type f))"

test:
	go test ./...

vet:
	go vet ./...

verify: fmt-check vet test
