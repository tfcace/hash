//go:build darwin

package shell

import "golang.org/x/sys/unix"

// Termios ioctls for the stream key watcher.
const (
	ioctlGetTermios = unix.TIOCGETA
	ioctlSetTermios = unix.TIOCSETA
)
