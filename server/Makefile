GO ?= go

.PHONY: build test test-race vet benchmark check-deps clean

build:
	mkdir -p bin
	$(GO) build -trimpath -o bin/apphub-server ./cmd/apphub-server

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

benchmark:
	$(GO) test -run '^$$' -bench BenchmarkAppsListTwentyThousandVersions -benchmem ./internal/store

check-deps:
	GO="$(GO)" ./scripts/check-server-deps.sh

clean:
	$(GO) clean
