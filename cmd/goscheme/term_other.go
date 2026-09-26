//go:build !linux && !darwin

package main

import (
	"errors"
	"os"
)

// makeRaw is not available on this platform, so the REPL falls back to the
// canonical, line oriented reader.
func makeRaw(f *os.File) (func(), error) {
	return nil, errors.New("raw mode is not supported on this platform")
}

// inputPending is not available on this platform.
func inputPending(f *os.File) bool { return false }
