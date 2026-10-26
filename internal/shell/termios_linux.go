//go:build linux

package shell

import "golang.org/x/sys/unix"

// Termios ioctls for the stream key watcher.
const (
	ioctlGetTermios = unix.TCGETS
	ioctlSetTermios = unix.TCSETS
)
