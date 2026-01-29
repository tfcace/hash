//go:build darwin

package executor

import (
	"os"

	"golang.org/x/sys/unix"
)

// disablePTYOutputProcessing disables ONLCR and other output processing on a PTY.
// This prevents LF→CRLF translation which would corrupt piped binary data.
func disablePTYOutputProcessing(f *os.File) error {
	fd := int(f.Fd())
	termios, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return err
	}
	// Disable ONLCR (map NL to CR-NL on output) and other output processing
	termios.Oflag &^= unix.ONLCR | unix.OCRNL | unix.OPOST
	return unix.IoctlSetTermios(fd, unix.TIOCSETA, termios)
}
