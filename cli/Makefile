GO ?= go

.PHONY: build test test-race vet clean

build:
	mkdir -p bin
	$(GO) build -trimpath -o bin/apphub ./cmd/apphub

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

clean:
	$(GO) clean
