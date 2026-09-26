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
	// On the BSDs select reports readiness through the descriptor set and
	// only returns an error, unlike the Linux signature which also counts the
	// ready descriptors.
	if err := syscall.Select(fd+1, &fds, nil, nil, &tv); err != nil {
		return false
	}
	return fds.Bits[fd/32]&(1<<(uint(fd)%32)) != 0
}
