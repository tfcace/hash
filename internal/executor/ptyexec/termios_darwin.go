//go:build darwin

package ptyexec

import "golang.org/x/sys/unix"

// Termios ioctls, taking effect at once (no drain, no flush).
const (
	ioctlGetTermios = unix.TIOCGETA
	ioctlSetTermios = unix.TIOCSETA
)
