# SPDX-License-Identifier: MIT

# GoScheme — R7RS Scheme interpreter written in Go.

GO      ?= /usr/bin/go
BIN     ?= goscheme
BUILD   := .build
DIST    := dist
VERSION := $(shell cat cmd/goscheme/VERSION)

# The platforms that `make dist` produces binaries for.
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

# The platforms that also get a dynamic (cgo) build, which is the flavour with
# (goscheme ffi).  Each one needs a C compiler for the target; the compilers and
# the CC_<os>_<arch> overrides are in scripts/build-dist.sh.  Platforms without
# one are skipped with a message, so this list may name more than you can build.
DYNAMIC_PLATFORMS ?= linux/amd64 linux/arm64 windows/amd64

LDFLAGS := -s -w -X main.version=$(VERSION)
GOFLAGS := -trimpath

.PHONY: all build test test-short check-disasm fmt vet clean dist list-dist repl examples

all: build

## build: compile the interpreter for the host platform
build:
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD)/$(BIN) ./cmd/goscheme

## test: run the Go unit tests and the Scheme test suites
test:
	$(GO) test ./...

## test-short: skip the reference R7RS suite
test-short:
	$(GO) test -short ./...

## check-disasm: compile a program with the freshly built interpreter and read
## the bytecode back with scripts/scmc-disassemble.scm, asserting that what it
## printed is readable Scheme.  It is the one test of the disassembler, and of
## the bytecode format as an outside reader sees it.
check-disasm: build
	@$(BUILD)/$(BIN) compile test/scheme/r7rs-tests.scm -o $(BUILD)/check.scmc
	@$(BUILD)/$(BIN) scripts/scmc-disassemble.scm --check $(BUILD)/check.scmc
	@$(BUILD)/$(BIN) compile test/scheme/goscheme-tests.scm -o $(BUILD)/check-ext.scmc
	@$(BUILD)/$(BIN) scripts/scmc-disassemble.scm --check $(BUILD)/check-ext.scmc

## fmt: format the Go sources
fmt:
	$(GO) fmt ./...

## vet: run go vet
vet:
	$(GO) vet ./...

## repl: start an interactive session
repl: build
	./$(BUILD)/$(BIN)

## examples: run every example in examples/
examples: build
	@GOSCHEME=./$(BUILD)/$(BIN) ./examples/run-all.sh

## dist: build every platform into dist/, static and, where a C compiler exists, dynamic
dist:
	@GO=$(GO) DYNAMIC_PLATFORMS="$(DYNAMIC_PLATFORMS)" ./scripts/build-dist.sh

## list-dist: show the platform matrix, both flavours
list-dist:
	@for p in $(PLATFORMS); do echo goscheme-$${p%%/*}-$${p##*/}; done
	@for p in $(DYNAMIC_PLATFORMS); do echo goscheme-$${p%%/*}-$${p##*/}-dynamic; done

## clean: remove build artifacts
clean:
	rm -rf $(BUILD) $(DIST)
