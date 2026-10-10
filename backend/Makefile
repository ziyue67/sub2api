.PHONY: build build-linux build-windows build-darwin generate test test-unit test-integration test-e2e

VERSION ?= $(shell ./scripts/resolve-version.sh)
LDFLAGS ?= -s -w -X main.Version=$(VERSION)

build:
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -trimpath -o bin/server ./cmd/server

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags embed -ldflags="$(LDFLAGS)" -trimpath -o sub2api ./cmd/server

build-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -tags embed -ldflags="$(LDFLAGS)" -trimpath -o sub2api ./cmd/server

build-darwin:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -tags embed -ldflags="$(LDFLAGS)" -trimpath -o sub2api ./cmd/server

generate:
	go generate ./ent
	go generate ./cmd/server

test:
	go test ./...
	golangci-lint run ./...

test-unit:
	go test -tags=unit ./...

test-integration:
	go test -tags=integration ./...

test-e2e:
	./scripts/e2e-test.sh

test-e2e-local:
	go test -tags=e2e -v -timeout=300s ./internal/integration/...
