VERSION := $(shell git describe --tags --always --dirty 2> NUL || echo dev)
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: build test lint clean

build:
	CGO_ENABLED=1 go build $(LDFLAGS) -o bin/dfmc ./cmd/dfmc

test:
	CGO_ENABLED=1 go test -race -count=1 ./...

lint:
	go vet ./...

clean:
	@if exist bin rmdir /s /q bin
