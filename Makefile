VERSION := $(shell git describe --tags --always --dirty 2> NUL || echo dev)
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"
GO_PACKAGES := $(filter-out %/node_modules/%,$(shell go list ./...))

.PHONY: build build-cgo test test-race lint vuln security clean

build:
	go build $(LDFLAGS) -o bin/dfmc ./cmd/dfmc

build-cgo:
	CGO_ENABLED=1 go build $(LDFLAGS) -o bin/dfmc ./cmd/dfmc

test:
	go test -count=1 $(GO_PACKAGES)

test-race:
	CGO_ENABLED=1 go test -race -count=1 $(GO_PACKAGES)

lint:
	go vet $(GO_PACKAGES)
	staticcheck $(GO_PACKAGES)
	golangci-lint run $(GO_PACKAGES)

vuln:
	govulncheck $(GO_PACKAGES)

security: vuln
	gosec $(GO_PACKAGES)

clean:
	@if exist bin rmdir /s /q bin
