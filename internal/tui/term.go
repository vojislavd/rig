// Package tui is rig's terminal dashboard ("rig" with no arguments). It
// is a thin client: it shows "rig status --json" and runs rig commands.
// Written with the standard library only, so rig keeps zero third-party
// modules.
package tui

import (
	"syscall"
	"unsafe"
)

// ioctl calls the ioctl system call on fd.
func ioctl(fd int, req uint, arg unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(req), uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}

// IsTerminal reports whether fd is a terminal.
func IsTerminal(fd int) bool {
	var t syscall.Termios
	return ioctl(fd, syscall.TCGETS, unsafe.Pointer(&t)) == nil
}

// rawMode puts the terminal into raw mode: keys arrive one by one, are
// not echoed, and Ctrl+C arrives as a key instead of killing rig (so the
// terminal is always restored). It returns a function that restores the
// previous settings.
func rawMode(fd int) (restore func(), err error) {
	var old syscall.Termios
	if err := ioctl(fd, syscall.TCGETS, unsafe.Pointer(&old)); err != nil {
		return nil, err
	}
	t := old
	t.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	t.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	t.Cflag &^= syscall.CSIZE | syscall.PARENB
	t.Cflag |= syscall.CS8
	t.Cc[syscall.VMIN] = 1
	t.Cc[syscall.VTIME] = 0
	if err := ioctl(fd, syscall.TCSETS, unsafe.Pointer(&t)); err != nil {
		return nil, err
	}
	return func() { ioctl(fd, syscall.TCSETS, unsafe.Pointer(&old)) }, nil
}

// size returns the terminal's width and height, or 80x24 if unknown.
func size(fd int) (width, height int) {
	var ws struct{ Row, Col, X, Y uint16 }
	if err := ioctl(fd, syscall.TIOCGWINSZ, unsafe.Pointer(&ws)); err != nil || ws.Col == 0 {
		return 80, 24
	}
	return int(ws.Col), int(ws.Row)
}

// Terminal escape codes.
const (
	altScreenOn  = "\x1b[?1049h"
	altScreenOff = "\x1b[?1049l"
	cursorHide   = "\x1b[?25l"
	cursorShow   = "\x1b[?25h"
	clearScreen  = "\x1b[H\x1b[2J"
)
