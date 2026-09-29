// SPDX-License-Identifier: MIT

package main

import (
	_ "embed"
	"strings"
)

// VERSION is the canonical version of the interpreter.  It lives next to the
// command so that it can be embedded: a plain
//
//	go build ./cmd/goscheme
//
// then still reports a real version instead of "dev".  A build may override it
// explicitly with
//
//	go build -ldflags "-X main.version=1.2.3" ./cmd/goscheme
//
//go:embed VERSION
var versionFile string

// version is the override target for -ldflags -X; when it is empty the
// embedded VERSION file is used.  It must stay initialised to a constant
// string expression, otherwise the linker cannot set it.
var version = ""

// release is the R7RS banner suffix.
const release = "R7RS"

// versionString renders the version banner.
func versionString() string {
	v := version
	if v == "" {
		v = strings.TrimSpace(versionFile)
	}
	if v == "" {
		v = "unknown"
	}
	return "GoScheme " + v + " (" + release + ")"
}
