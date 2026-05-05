VERSION := $(shell git describe --tags --always --dirty 2> NUL || echo dev)
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: build build-cgo test test-race lint vuln clean

build:
	go build $(LDFLAGS) -o bin/dfmc ./cmd/dfmc

build-cgo:
	CGO_ENABLED=1 go build $(LDFLAGS) -o bin/dfmc ./cmd/dfmc

test:
	go test -count=1 ./...

test-race:
	CGO_ENABLED=1 go test -race -count=1 ./...

lint:
	go vet ./...
	staticcheck ./...

vuln:
	govulncheck ./...

clean:
	@if exist bin rmdir /s /q bin
