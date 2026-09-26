//go:build linux

package main

import (
	"os"
	"syscall"
	"unsafe"
)

func ioctl(fd int, req uintptr, arg unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}

// makeRaw puts the terminal into raw mode: no canonical line buffering, no
// echo and no signal generation.  It returns a function that restores the
// previous settings.
//
// Raw mode is what makes bracketed paste usable: in canonical mode the closing
// marker of a paste sits in the line buffer until a newline arrives, so the
// interpreter would either block or guess.  Reading byte by byte removes the
// guesswork.
func makeRaw(f *os.File) (func(), error) {
	fd := int(f.Fd())
	var old syscall.Termios
	if err := ioctl(fd, syscall.TCGETS, unsafe.Pointer(&old)); err != nil {
		return nil, err
	}
	raw := old
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	// OPOST is deliberately left alone so that a plain "\n" still becomes
	// CR+LF on output.
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := ioctl(fd, syscall.TCSETS, unsafe.Pointer(&raw)); err != nil {
		return nil, err
	}
	return func() { _ = ioctl(fd, syscall.TCSETS, unsafe.Pointer(&old)) }, nil
}

// inputPending reports whether more input is already waiting on the terminal.
// It is only used by the canonical-mode fallback.
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
