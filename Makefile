BIN := browser-mcp
GO ?= go

.PHONY: build test vet fmt icons runtime-test all

all: build test

build:
	$(GO) build -o $(BIN) ./cmd/browser-mcp

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -w .

icons:
	python3 extension/make_icons.py

runtime-test:
	node extension/test_runtime.js