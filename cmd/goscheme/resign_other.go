// SPDX-License-Identifier: MIT

//go:build !darwin

package main

// resignIfNeeded does nothing away from macOS: ELF and PE images do not carry
// a signature that appending to them would invalidate.
func resignIfNeeded(path string) {}
