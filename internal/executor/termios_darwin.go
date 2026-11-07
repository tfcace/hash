//go:build darwin

package executor

import "golang.org/x/sys/unix"

// Termios ioctls, for read -s on a terminal.
const (
	ioctlGetTermios = unix.TIOCGETA
	ioctlSetTermios = unix.TIOCSETA
)
