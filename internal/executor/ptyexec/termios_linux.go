//go:build linux

package ptyexec

import "golang.org/x/sys/unix"

// Termios ioctls, taking effect at once (no drain, no flush).
const (
	ioctlGetTermios = unix.TCGETS
	ioctlSetTermios = unix.TCSETS
)
