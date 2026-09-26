//go:build linux

package main

import (
	"os"
	"syscall"
)

// inputPending reports whether more input is already waiting on the terminal.
//
// This is what keeps a pasted multi-line form from being sprayed with prompts:
// the kernel holds the rest of the paste, so the interpreter knows it does not
// have to wait for the user.  In canonical mode the line discipline only
// reports readiness once a complete line has been entered, so a partially
// typed line does not suppress the prompt.
func inputPending(f *os.File) bool {
	fd := int(f.Fd())
	if fd < 0 {
		return false
	}
	var fds syscall.FdSet
	fds.Bits[fd/64] |= 1 << (uint(fd) % 64)
	tv := syscall.Timeval{}
	n, err := syscall.Select(fd+1, &fds, nil, nil, &tv)
	return err == nil && n > 0
}
