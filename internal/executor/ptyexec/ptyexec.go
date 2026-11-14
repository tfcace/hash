// Package ptyexec runs a command on a PTY through hash itself, so the keys
// the user typed while it ran that it left unread come back to the shell.
//
// A command at the interactive prompt whose stdin and stdout are the
// terminal runs on a PTY of hash's, and hash copies the terminal's keys to
// it. Keys the command never reads (`echo hi⏎` typed during `sleep 1`) sit
// in the PTY's input queue; in bash they stay on the terminal for the next
// prompt. When the PTY's session leader exits, macOS revokes the terminal
// and its queue with it, so hash cannot read them from the PTY after the
// command's Wait. Instead hash starts itself (Args) as the PTY's session
// leader and controlling process, and this package's init, in that
// process, runs the command as a child in a process group of its own, the
// PTY's foreground group (so Ctrl+C and Ctrl+\ reach it alone), passes on
// to it the signals hash sends its own group and the PTY's hangup (hash
// died: on Linux the hangup reaches the session leader alone), waits for
// it, and then, with the session still held, takes the foreground back for
// a group that holds only itself, reads what is left on the PTY (a partial
// line too: canonical mode off) and reports it to hash on an inherited
// descriptor (Report). The hangup of its exit reaches only that group: a
// background child the command left running lives on, as in bash. It
// reads only once hash stopped copying the terminal's keys to the PTY: it reports that the
// command ended and waits for hash's release, so no key reaches the PTY
// after the read, to be lost with it. It then exits with the command's
// status, or dies of the signal the command died of (a SIGINT death is a
// Ctrl+C for hash's line).
//
// The init runs in any program that links the package (hash, and the test
// binaries of the packages that run PTY commands), before main. It does
// nothing unless the program was started by Args.
package ptyexec

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// token is the first argument that makes a hash the session leader of a
// PTY command; no one types it.
const token = "__hash_pty_exec__"

// reportFD is the descriptor the started hash reports on: the first of
// the command's ExtraFiles. releaseFD, the second, is the read end of the
// pipe whose write end hash closes to release it (Args).
const (
	reportFD  = 3
	releaseFD = 4
)

// releaseWait bounds the started hash's wait for its release: a hash that
// does not release it (stuck, or one of another build) must not keep the
// command's end, and the read of the keys left on the PTY, from it. hash's
// copy never blocks (it writes to the PTY only when there is room), so it
// releases as soon as it stopped.
const releaseWait = 2 * time.Second

// maxUnread bounds what the started hash reads off the PTY: more than a
// terminal's input queue holds.
const maxUnread = 64 << 10

// The report's kinds, its first byte.
const (
	reportStarted   = 's' // the command started; a reportEnded follows when it ended
	reportEnded     = 'e' // the command ended; the started hash waits for its release
	reportUnread    = 'u' // the rest is what the command left unread
	reportStartFail = 'x' // the rest is the errno of the command's failed exec
)

// Args is the argv for the program self (hash) that runs the program at
// path with argv (argv[0] its name as typed) on the PTY its stdin is. The
// caller passes the write end of a pipe as the command's first ExtraFiles
// entry and reads the report from the other end (Report), and the read end
// of another as the second, whose write end it closes once the command
// ended (Report's ended) and it stopped writing to the PTY.
func Args(self, path string, argv []string) []string {
	return append([]string{self, token, path}, argv...)
}

func init() {
	if len(os.Args) < 4 || os.Args[1] != token {
		return
	}
	run(os.Args[2], os.Args[3:])
}

// run runs path with argv to its end, reports, and ends as the command
// ended. It does not return.
func run(path string, argv []string) {
	report := os.NewFile(reportFD, "report")
	syscall.CloseOnExec(reportFD) // the command's own children never hold them
	syscall.CloseOnExec(releaseFD)
	// The signals hash sends this process's group (a double Ctrl+C, a
	// canceled line) are the command's: this process passes them on and
	// waits for it to decide. Caught, not ignored, so the command gets
	// them at their defaults. Queued from now: one sent before the
	// command started reaches it once it did.
	sigs := make(chan os.Signal, 8)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP)
	cmd := &exec.Cmd{
		Path: path, Args: argv, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		// A group of its own, the PTY's foreground, made so before its
		// exec: it reads the terminal from its first instruction.
		SysProcAttr: &syscall.SysProcAttr{Foreground: true, Ctty: int(os.Stdin.Fd())},
	}
	if err := cmd.Start(); err != nil {
		var errno syscall.Errno
		if !errors.As(err, &errno) {
			errno = syscall.EINVAL
		}
		_, _ = report.Write(append([]byte{reportStartFail}, strconv.Itoa(int(errno))...))
		os.Exit(126)
	}
	_, _ = report.Write([]byte{reportStarted})
	go passOn(sigs, cmd.Process.Pid)
	status := waitGoingOn(cmd.Process.Pid)
	_, _ = report.Write([]byte{reportEnded})
	awaitRelease(releaseFD)
	_, _ = report.Write(append([]byte{reportUnread}, unread(int(os.Stdin.Fd()))...))
	_ = report.Close()
	endAs(status)
}

// waitGoingOn waits for the command, the process pid, to end, and
// continues its group whenever it stops (Ctrl+Z, a TSTP of its own): hash
// has no job control for a PTY command, which stopped would hold the
// shell for good.
func waitGoingOn(pid int) syscall.WaitStatus {
	for {
		var ws syscall.WaitStatus
		_, err := syscall.Wait4(pid, &ws, syscall.WUNTRACED, nil)
		switch {
		case errors.Is(err, syscall.EINTR):
			continue
		case err == nil && ws.Stopped():
			_ = syscall.Kill(-pid, syscall.SIGCONT)
			continue
		}
		return ws
	}
}

// passOn sends the signals that come on sigs to the command's process
// group, group. A hangup of the PTY (hash ended, its master closed) is
// passed on too: on Linux it reaches the session leader alone, and the
// command would run on, for good. (On macOS the command, the terminal's
// foreground, had it already; a second is the same end.)
func passOn(sigs <-chan os.Signal, group int) {
	for sig := range sigs {
		if s, ok := sig.(syscall.Signal); ok {
			_ = syscall.Kill(-group, s)
		}
	}
}

// awaitRelease waits until hash closes the write end of the pipe whose
// read end is fd (or ends), or for releaseWait.
func awaitRelease(fd int) {
	deadline := time.Now().Add(releaseWait)
	for {
		wait := time.Until(deadline)
		if wait <= 0 {
			return
		}
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}} //nolint:gosec // G115: a descriptor number
		if _, err := unix.Poll(fds, int(wait.Milliseconds())+1); !errors.Is(err, unix.EINTR) {
			return
		}
	}
}

// unread reads what is left in the input queue of the terminal at fd
// without waiting: the terminal is made the foreground of this process's
// group again, which holds only it (the command's group is background
// from then: the hangup of this process's exit does not reach it), and
// non-canonical with no minimum, so a line without its Enter is read too.
// SIGTTOU and SIGTTIN are ignored first: a process outside the foreground
// group would stop at either, for good.
func unread(fd int) []byte {
	signal.Ignore(syscall.SIGTTOU, syscall.SIGTTIN)
	_ = unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, syscall.Getpgrp())
	t, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return nil
	}
	t.Lflag &^= unix.ICANON
	t.Cc[unix.VMIN], t.Cc[unix.VTIME] = 0, 0
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, t); err != nil {
		return nil
	}
	var left []byte
	buf := make([]byte, 4096)
	for len(left) < maxUnread {
		n, err := syscall.Read(fd, buf)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if n <= 0 {
			break
		}
		left = append(left, buf[:n]...)
	}
	return left
}

// endAs ends this process as the command ended (ws): with its exit status,
// or of its signal. The Go runtime dies of SIGINT, SIGTERM and SIGHUP when nothing
// catches them (and of SIGKILL); any other signal it would take as a crash
// of its own or ignore, so for those the process exits 128 plus the
// signal, the status a shell sets for the death all the same.
func endAs(ws syscall.WaitStatus) {
	if !ws.Signaled() {
		os.Exit(ws.ExitStatus())
	}
	sig := ws.Signal()
	switch sig {
	case syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGKILL:
		signal.Reset(sig)
		_ = syscall.Kill(os.Getpid(), sig)
		time.Sleep(time.Second) // the signal is on its way
	}
	os.Exit(128 + int(sig))
}

// ErrCutShort is Report's error for a report that ended before its last
// word: the started hash was killed, or is not one that reports.
var ErrCutShort = errors.New("pty helper: report cut short")

// Report reads the started hash's report from r, the read end of the pipe
// whose write end it inherited, to its end. It calls started as the
// report says the command started (its exec succeeded) and ended as it
// says the command ended (the started hash then waits for its release),
// each as often as the report says it, and returns what the command left
// unread; or a *os.PathError for its failed exec of path, as
// os.StartProcess reports one; or ErrCutShort for a report with no last
// word.
func Report(r io.Reader, path string, started, ended func()) (left []byte, err error) {
	br := bufio.NewReader(r)
	for {
		var k [1]byte
		if n, _ := io.ReadFull(br, k[:]); n == 0 {
			return nil, ErrCutShort
		}
		kind := k[0]
		switch kind {
		case reportStarted:
			started()
			continue
		case reportEnded:
			ended()
			continue
		}
		rest, _ := io.ReadAll(io.LimitReader(br, maxUnread))
		switch kind {
		case reportUnread:
			return rest, nil
		case reportStartFail:
			n, err := strconv.Atoi(string(rest))
			if err != nil {
				return nil, fmt.Errorf("pty helper: bad report %q", append([]byte{kind}, rest...))
			}
			return nil, &os.PathError{Op: "fork/exec", Path: path, Err: syscall.Errno(n)}
		}
		return nil, fmt.Errorf("pty helper: bad report %q", append([]byte{kind}, rest...))
	}
}
