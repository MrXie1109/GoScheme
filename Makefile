# GoScheme — R7RS Scheme interpreter written in Go.

GO      ?= /usr/bin/go
BIN     ?= goscheme
BUILD   := .build
DIST    := dist
VERSION := 1.0

# The platforms that `make dist` produces binaries for.
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

LDFLAGS := -s -w
GOFLAGS := -trimpath

.PHONY: all build test test-short fmt vet clean dist list-dist repl

all: build

## build: compile the interpreter for the host platform
build:
	$(GO) build $(GOFLAGS) -o $(BUILD)/$(BIN) ./cmd/goscheme

## test: run the Go unit tests and the Scheme test suites
test:
	$(GO) test ./...

## test-short: skip the reference R7RS suite
test-short:
	$(GO) test -short ./...

## fmt: format the Go sources
fmt:
	$(GO) fmt ./...

## vet: run go vet
vet:
	$(GO) vet ./...

## repl: start an interactive session
repl: build
	./$(BUILD)/$(BIN)

## dist: cross compile for every supported platform into dist/
dist:
	@mkdir -p $(DIST)
	@set -e; for p in $(PLATFORMS); do \
	  os=$${p%%/*}; arch=$${p##*/}; \
	  out=$(DIST)/goscheme-$$os-$$arch; \
	  if [ "$$os" = "windows" ]; then out=$$out.exe; fi; \
	  echo "building $$out"; \
	  GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $$out ./cmd/goscheme; \
	done
	@cd $(DIST) && sha256sum * > SHA256SUMS 2>/dev/null || true
	@ls -l $(DIST)

## list-dist: show the platform matrix
list-dist:
	@for p in $(PLATFORMS); do echo goscheme-$${p%%/*}-$${p##*/}; done

## clean: remove build artifacts
clean:
	rm -rf $(BUILD) $(DIST)
