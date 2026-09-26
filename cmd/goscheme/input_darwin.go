//go:build darwin

package main

import (
	"os"
	"syscall"
)

// inputPending reports whether more input is already waiting on the terminal.
// See the Linux version for the rationale.
func inputPending(f *os.File) bool {
	fd := int(f.Fd())
	if fd < 0 {
		return false
	}
	var fds syscall.FdSet
	fds.Bits[fd/32] |= 1 << (uint(fd) % 32)
	tv := syscall.Timeval{}
	n, err := syscall.Select(fd+1, &fds, nil, nil, &tv)
	return err == nil && n > 0
}
