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

// setInterrupts is not available on this platform; signal generation is left
// as the platform set it.
func setInterrupts(f *os.File, on bool) error { return nil }

// inputPending is not available on this platform.
func inputPending(f *os.File) bool { return false }
