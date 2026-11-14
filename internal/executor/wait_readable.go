package executor

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
	"mvdan.cc/sh/v3/interp"
)

// readablePoll is how often WaitReadable looks at its context: a wait
// ends up to this long after the context did (a fatal signal, Ctrl+C at a
// read). Data ends it at once.
const readablePoll = 50 * time.Millisecond

// WaitReadable waits until f has data to read (or its writer closed it), or
// ctx ends, which it returns. A read of a blocking descriptor, such as the
// stdin hash inherits, cannot be stopped once it started, so a read that
// must end on a fatal signal waits here first. A file it cannot watch (no
// descriptor, or one past select's limit) counts as readable.
func WaitReadable(ctx context.Context, f *os.File) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if readableWithin(f, readablePoll) {
			return nil
		}
	}
}

// readableWithin reports whether f has data within timeout; true for a file
// it cannot watch.
func readableWithin(f *os.File, timeout time.Duration) bool {
	rc, err := f.SyscallConn()
	if err != nil {
		return true
	}
	ready := true
	// Control leaves the descriptor's blocking mode alone, unlike Fd.
	_ = rc.Control(func(fd uintptr) {
		if fd >= unix.FD_SETSIZE {
			return
		}
		var set unix.FdSet
		set.Set(int(fd))
		tv := unix.NsecToTimeval(timeout.Nanoseconds())
		_, err := unix.Select(int(fd)+1, &set, nil, nil, &tv)
		// A signal cut the wait short: look again. Any other error is
		// the read's to report.
		switch err {
		case unix.EINTR:
			ready = false
		case nil:
			ready = set.IsSet(int(fd))
		}
	})
	return ready
}

// isTerminal reports whether f is a terminal. Unlike term.IsTerminal on
// f.Fd(), it leaves f's mode alone: Fd puts a pollable file (a pipe or
// FIFO hash opened) in blocking mode, where reads take no deadline and the
// interpreter can no longer cancel them.
func isTerminal(f *os.File) bool {
	rc, err := f.SyscallConn()
	if err != nil {
		return false
	}
	is := false
	_ = rc.Control(func(fd uintptr) { is = term.IsTerminal(int(fd)) })
	return is
}

// readCall is the call to run for the read builtin. The interpreter
// cancels a read only on a descriptor that takes deadlines, which hash's
// inherited stdin does not, so hash holds the read until its stdin has data
// and runs it then, or, once the line is canceled (a fatal signal, Ctrl+C in
// a script), runs a failing call instead. (A descriptor that takes
// deadlines it leaves to the interpreter.)
//
// The prompt (-p) shows only on a terminal, as in bash; the builtin would
// print it into stdout whatever stdin is.
//
// On a terminal the wait comes after what the builtin would do before
// reading: hash prints the -p prompt itself (to stderr, as bash) and turns
// the echo off for -s, since the terminal echoes a line as it is typed and
// lets the read have it only at Enter.
//
// A job in a process group of its own (jobsApart) is not the terminal's
// foreground: its read of the terminal fails, as the read of a job's
// command does (EIO, with SIGTTIN ignored), where hash, the foreground,
// would take the keys typed at the prompt.
func readCall(ctx context.Context, args []string, jobsApart bool) []string {
	call, ok := parseRead(args)
	if !ok {
		return args // a bad option: the builtin reports it
	}
	hc := interp.HandlerCtx(ctx)
	f, isFile := hc.Stdin.(*os.File)
	if !isFile || !isTerminal(f) {
		// No prompt: bash prints one only for a terminal.
		if isFile && f.SetReadDeadline(time.Time{}) != nil && WaitReadable(ctx, f) != nil {
			return []string{"false"}
		}
		return call.args
	}
	if jobsApart && inBackgroundJob(ctx) {
		fmt.Fprintln(hc.Stderr, "hash: read: read error: 0: Input/output error")
		return statusCall(1)
	}
	if call.prompt != "" {
		_, _ = io.WriteString(hc.Stderr, call.prompt)
	}
	if call.silent {
		defer echoOff(f)()
	}
	if WaitReadable(ctx, f) != nil {
		return []string{"false"}
	}
	return call.args
}

// readArgs is a read call taken apart: its prompt and -s, and the call
// without the prompt.
type readArgs struct {
	prompt string
	silent bool
	args   []string
}

// parseRead takes a read call apart as the builtin parses it (-r, -s, -a,
// -p PROMPT, combined as in -rsp). ok is false for an option the builtin
// rejects.
func parseRead(args []string) (call readArgs, ok bool) {
	call.args = []string{"read"}
	rest := args[1:]
	for len(rest) > 0 && rest[0] != "" && (rest[0][0] == '-' || rest[0][0] == '+') {
		arg := rest[0]
		rest = rest[1:]
		if arg == "--" {
			break
		}
		if arg[0] == '+' || len(arg) == 1 {
			return call, false
		}
		for _, c := range arg[1:] {
			switch c {
			case 'r', 'a', 's':
				call.args = append(call.args, "-"+string(c))
				call.silent = call.silent || c == 's'
			case 'p':
				if len(rest) == 0 || rest[0] == "" {
					return call, false
				}
				call.prompt, rest = rest[0], rest[1:]
			default:
				return call, false
			}
		}
	}
	call.args = append(append(call.args, "--"), rest...)
	return call, true
}

// echoOff turns f's echo off, and returns the function that turns it back
// on; a no-op when f's modes cannot be read.
func echoOff(f *os.File) (restore func()) {
	fd := int(f.Fd())
	old, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return func() {}
	}
	quiet := *old
	quiet.Lflag &^= unix.ECHO
	if unix.IoctlSetTermios(fd, ioctlSetTermios, &quiet) != nil {
		return func() {}
	}
	return func() { _ = unix.IoctlSetTermios(fd, ioctlSetTermios, old) }
}
