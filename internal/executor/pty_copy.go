package executor

import (
	"errors"
	"io"
	"os"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// errCopyDone ends a copy that was told to stop.
var errCopyDone = errors.New("copy done")

// maxHeldKeys bounds the keys the copy holds for a PTY that takes none:
// past it they wait on the terminal, the next prompt's all the same.
const maxHeldKeys = 64 << 10

// keyCopy copies the terminal's keys to a PTY command from its start until
// stop. It waits at once for a key, for room on the PTY when it holds keys
// the PTY's input queue had no room for, and for its stop (poll on the
// terminal, the PTY and a pipe stop closes), so it never blocks in a
// write, stops as soon as it is told to, and a key typed after that stays
// on the terminal: the next prompt's. The keys it read and could not
// write are stop's to return, for the next prompt after the ones the
// command left on the PTY. Once it stopped, and only then, it releases
// the PTY's leader to read those (ptyexec): no key reaches the PTY after.
type keyCopy struct {
	stopW    *os.File      // closed to stop the copy; nil: it never ran (no pipe to be had)
	stopped  chan struct{} // closed once the copy returned and released
	stopOnce sync.Once
	held     []byte // the keys read and never written, once stopped
}

// keyHooks are what a keyCopy calls: onInput with each read, onWrite with
// the size of each write, ended with the copy's end (errCopyDone once
// stopped). Any may be nil.
type keyHooks struct {
	onInput func([]byte)
	onWrite func(int)
	ended   func(error)
}

// startKeyCopy starts copying the keys at the terminal src to the PTY
// master dst (non-blocking, see nonblockingFD), first held, the keys the
// command has before any is typed, and closes release (when not nil) once
// it stopped. With no pipe to be had (out of descriptors) it copies
// nothing, and holds held.
func startKeyCopy(dst int, src *os.File, held []byte, release io.Closer, hooks keyHooks) *keyCopy {
	c := &keyCopy{stopped: make(chan struct{}), held: held}
	stopR, stopW, err := os.Pipe()
	if err != nil {
		c.end(err, release, hooks)
		return c
	}
	c.stopW = stopW
	go func() {
		defer stopR.Close()
		err := errCopyDone
		if srcFD, ok := fdOf(src); ok {
			c.held, err = copyKeys(dst, srcFD, int(stopR.Fd()), c.held, hooks.onInput, hooks.onWrite)
		}
		c.end(err, release, hooks)
	}()
	return c
}

// end ends the copy: it is stopped, then the leader released.
func (c *keyCopy) end(err error, release io.Closer, hooks keyHooks) {
	if hooks.ended != nil {
		hooks.ended(err)
	}
	if release != nil {
		_ = release.Close()
	}
	close(c.stopped)
}

// stop stops the copy, waits until it returned (and released the
// leader), and returns the keys it read and could not write to the PTY.
func (c *keyCopy) stop() (held []byte) {
	c.stopOnce.Do(func() {
		if c.stopW != nil {
			_ = c.stopW.Close()
		}
	})
	<-c.stopped
	return c.held
}

// copyKeys copies what can be read at src to dst, after held, until stop
// is readable (its write end closed), and returns what it read and did
// not write. A stop at once with a key, or with room on dst, is a stop.
func copyKeys(dst, src, stop int, held []byte, onInput func([]byte), onWrite func(int)) ([]byte, error) {
	buf := make([]byte, 4096)
	fds := make([]unix.PollFd, 3)
	for {
		fds[0] = pollFD(stop, unix.POLLIN, true)
		fds[1] = pollFD(src, unix.POLLIN, len(held) < maxHeldKeys)
		fds[2] = pollFD(dst, unix.POLLOUT, len(held) > 0)
		if err := pollAll(fds); err != nil {
			return held, err
		}
		if fds[0].Revents != 0 {
			return held, errCopyDone
		}
		var err error
		if fds[2].Revents != 0 {
			if held, err = writeHeld(dst, held, onWrite); err != nil {
				return held, err
			}
		}
		if fds[1].Revents != 0 {
			if held, err = readKeys(src, buf, held, onInput); err != nil {
				return held, err
			}
		}
	}
}

// writeHeld writes what of held dst has room for, and returns the rest.
func writeHeld(dst int, held []byte, onWrite func(int)) ([]byte, error) {
	n, err := unix.Write(dst, held)
	if n > 0 {
		held = held[n:]
		if onWrite != nil {
			onWrite(n)
		}
	}
	if err != nil && !retryable(err) {
		return held, err
	}
	return held, nil
}

// readKeys reads the keys at src into buf and returns held with them.
func readKeys(src int, buf, held []byte, onInput func([]byte)) ([]byte, error) {
	n, err := unix.Read(src, buf)
	if n > 0 {
		if onInput != nil {
			onInput(buf[:n])
		}
		held = append(held, buf[:n]...)
	}
	switch {
	case retryable(err):
		return held, nil
	case err != nil:
		return held, err
	case n == 0:
		return held, io.EOF
	}
	return held, nil
}

// pollFD is the poll entry that waits for events at fd, or, when not
// wanted, one poll leaves alone.
func pollFD(fd int, events int16, wanted bool) unix.PollFd {
	if !wanted {
		return unix.PollFd{Fd: -1}
	}
	return unix.PollFd{Fd: int32(fd), Events: events} //nolint:gosec // G115: a descriptor number
}

// pollAll waits until one of fds has an event, through signals.
func pollAll(fds []unix.PollFd) error {
	_, err := pollFor(fds, -1)
	return err
}

// outputCopy copies what a PTY command shows to the terminal, from its
// start until the PTY's terminal ends, or, once told the command ended
// (end), until the PTY has shown nothing for ptyStopGrace (a child the
// command left holds its terminal), or for ptyDrainCap at the most (a
// child that never stops writing to it). The time the terminal takes to
// show what the copy read is no time the PTY is quiet: a terminal that
// reads slowly gets all of it.
type outputCopy struct {
	// wakeW is closed by end to wake the copy; nil with no pipe to be had
	// (out of descriptors): the copy then looks for its end every
	// ptyStopGrace.
	wakeW   *os.File
	ending  chan struct{}
	endOnce sync.Once
	done    chan struct{} // closed once the copy ended, with err
	// err is the copy's end, once done is closed: nil once the PTY's
	// terminal ended, errCopyDone once cut off after the command's end.
	err error
}

// startOutputCopy starts copying what the PTY master src (non-blocking)
// shows to dst (copyOutput).
func startOutputCopy(dst io.Writer, src int, onRead, onWrite func(int)) *outputCopy {
	c := &outputCopy{ending: make(chan struct{}), done: make(chan struct{})}
	wakeR, wakeW, err := os.Pipe()
	wake := -1
	if err == nil {
		c.wakeW = wakeW
		wake = int(wakeR.Fd())
	}
	go func() {
		if wakeR != nil {
			defer wakeR.Close()
		}
		c.err = copyOutput(dst, src, wake, c.ending, onRead, onWrite)
		close(c.done)
	}()
	return c
}

// wait waits for the copy to end and returns its end (err).
func (c *outputCopy) wait() error {
	<-c.done
	return c.err
}

// end tells the copy the command ended; it then ends soon (done).
func (c *outputCopy) end() {
	c.endOnce.Do(func() {
		close(c.ending)
		if c.wakeW != nil {
			_ = c.wakeW.Close()
		}
	})
}

// copyOutput copies what the PTY master src (non-blocking) shows to dst
// until its terminal ends (no one holds it), or, once ending is closed,
// until src was quiet for ptyStopGrace or ptyDrainCap passed
// (errCopyDone), calling onRead with each read and onWrite with each
// write. wake turns readable (its write end closed) as ending closes;
// with no wake (-1) the copy looks at ending every ptyStopGrace.
func copyOutput(dst io.Writer, src, wake int, ending <-chan struct{}, onRead, onWrite func(int)) error {
	buf := make([]byte, 32<<10)
	fds := []unix.PollFd{pollFD(wake, unix.POLLIN, wake >= 0), pollFD(src, unix.POLLIN, true)}
	var deadline time.Time // once the command ended: the copy's end at the latest
	for {
		timeout := -1
		if wake < 0 {
			timeout = int(ptyStopGrace.Milliseconds())
		}
		if deadline.IsZero() && isClosed(ending) {
			deadline = time.Now().Add(ptyDrainCap)
			fds[0] = pollFD(wake, unix.POLLIN, false)
		}
		if !deadline.IsZero() {
			left := time.Until(deadline)
			if left <= 0 {
				return errCopyDone
			}
			timeout = int(min(left, ptyStopGrace).Milliseconds())
		}
		n, err := pollFor(fds, timeout)
		switch {
		case err != nil:
			return err
		case n == 0 && !deadline.IsZero():
			return errCopyDone // quiet, or past the deadline
		case fds[1].Revents == 0:
			continue // woken, or nothing yet
		}
		if done, err := copyRead(dst, src, buf, onRead, onWrite); done {
			return err
		}
	}
}

// copyRead copies what one read of src gives to dst; done once the copy
// ended, with its end (nil: the terminal ended).
func copyRead(dst io.Writer, src int, buf []byte, onRead, onWrite func(int)) (done bool, err error) {
	n, err := unix.Read(src, buf)
	if n > 0 {
		if onRead != nil {
			onRead(n)
		}
		if werr := writeAll(dst, buf[:n]); werr != nil {
			return true, werr
		}
		if onWrite != nil {
			onWrite(n)
		}
	}
	switch {
	case retryable(err):
		return false, nil
	case errors.Is(err, syscall.EIO), err == nil && n == 0:
		return true, nil // the terminal ended
	}
	return err != nil, err
}

// isClosed reports whether ch is closed.
func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// pollFor waits until one of fds has an event, through signals, or for
// timeout milliseconds (-1: no end), and returns how many have one.
func pollFor(fds []unix.PollFd, timeout int) (int, error) {
	for {
		n, err := unix.Poll(fds, timeout)
		if !errors.Is(err, unix.EINTR) {
			return n, err
		}
	}
}

// retryable reports whether a read or write that failed with err may be
// tried again: a signal cut it short, or a non-blocking descriptor had
// nothing (no room) for it.
func retryable(err error) bool {
	return errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN)
}

// nonblockingFD is the PTY master ptmx's descriptor, made non-blocking for
// the copies (keyCopy, copyOutput), which wait with poll: a write to a full
// input queue must not block. The descriptor is ptmx's while it is open.
func nonblockingFD(ptmx *os.File) int {
	fd, ok := fdOf(ptmx)
	if !ok {
		return -1 // not a descriptor: poll waits on it for nothing
	}
	_ = unix.SetNonblock(fd, true) // else a full queue blocks a write, for a while
	return fd
}

// fdOf is f's descriptor, which it leaves in the mode it is in (unlike
// Fd, which makes a pollable file blocking).
func fdOf(f *os.File) (int, bool) {
	if f == nil {
		return -1, false
	}
	rc, err := f.SyscallConn()
	if err != nil {
		return -1, false
	}
	fd := -1
	_ = rc.Control(func(d uintptr) { fd = int(d) })
	return fd, fd >= 0
}
