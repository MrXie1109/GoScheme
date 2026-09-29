// SPDX-License-Identifier: MIT

//go:build darwin

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

// makeRaw puts the terminal into raw mode; see the Linux version for why.
func makeRaw(f *os.File) (enter func(), restore func(), err error) {
	fd := int(f.Fd())
	var old syscall.Termios
	if err := ioctl(fd, syscall.TIOCGETA, unsafe.Pointer(&old)); err != nil {
		return nil, nil, err
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
	if err := ioctl(fd, syscall.TIOCSETA, unsafe.Pointer(&raw)); err != nil {
		return nil, nil, err
	}
	enter = func() { _ = ioctl(fd, syscall.TIOCSETA, unsafe.Pointer(&raw)) }
	restore = func() { _ = ioctl(fd, syscall.TIOCSETA, unsafe.Pointer(&old)) }
	return enter, restore, nil
}

// setInterrupts enables or disables the generation of SIGINT for Ctrl-C
// without disturbing the rest of the raw settings.  It is switched on while an
// evaluation runs, so that Ctrl-C aborts the evaluation, and off while a line
// is edited, where Ctrl-C is handled as an ordinary key.
func setInterrupts(f *os.File, on bool) error {
	fd := int(f.Fd())
	var t syscall.Termios
	if err := ioctl(fd, syscall.TIOCGETA, unsafe.Pointer(&t)); err != nil {
		return err
	}
	if on {
		t.Lflag |= syscall.ISIG
	} else {
		t.Lflag &^= syscall.ISIG
	}
	return ioctl(fd, syscall.TIOCSETA, unsafe.Pointer(&t))
}

// inputPending reports whether more input is already waiting on the terminal.
func inputPending(f *os.File) bool {
	fd := int(f.Fd())
	if fd < 0 {
		return false
	}
	var fds syscall.FdSet
	fds.Bits[fd/32] |= 1 << (uint(fd) % 32)
	tv := syscall.Timeval{}
	// On the BSDs select reports readiness through the descriptor set and
	// only returns an error.
	if err := syscall.Select(fd+1, &fds, nil, nil, &tv); err != nil {
		return false
	}
	return fds.Bits[fd/32]&(1<<(uint(fd)%32)) != 0
}
