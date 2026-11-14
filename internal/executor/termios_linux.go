//go:build linux

package executor

import "golang.org/x/sys/unix"

// Termios ioctls, for read -s on a terminal.
const (
	ioctlGetTermios = unix.TCGETS
	ioctlSetTermios = unix.TCSETS
)
