//go:build !linux && !darwin

package main

import "os"

// inputPending is not available on this platform; the REPL then relies on its
// own buffer to detect queued input.
func inputPending(f *os.File) bool { return false }
