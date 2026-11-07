package executor

import (
	"context"
	"errors"
	"syscall"

	"mvdan.cc/sh/v3/interp"
)

// LineEnd is how a line ended (Result.End). Execute works it out once, as
// the line returns (Executor.lineEnd); Result.ExitCode is the final status
// for each.
type LineEnd int

const (
	// LineReturned: the line ran to its end; ExitCode is its last
	// command's status.
	LineReturned LineEnd = iota
	// LineExited: the line ended the shell: it requested the shell's exit
	// (lineRun.requestExit: the exit builtin ran in the shell itself, not a
	// subshell, or an exec's command did), or errexit fired. ExitCode is
	// the shell's exit status.
	LineExited
	// LineFailed: a fatal error ended the line: an expansion error
	// (${x:?msg}, set -u and an unset variable), an error of hash's own,
	// the shell's end. The interactive shell goes on, a script ends, as in
	// bash. ExitCode is the status the error sets.
	LineFailed
	// LineInterrupted: Ctrl+C ended the line (ErrLineInterrupted); ExitCode
	// is 130.
	LineInterrupted
)

// expansionErrorStatus is the status of an expansion error, as bash 5.2
// sets it: 1 in the interactive shell, which goes on; 127 in a script,
// which it ends.
func (e *Executor) expansionErrorStatus() int {
	if e.terminal == terminalInteractive {
		return 1
	}
	return 127
}

// lineEnd is how the line lr, which ran on ctx, ended, and its status:
// runErr is what the interpreter's Run returned. The line's exit request
// comes first: the interpreter may not have seen the exit (one in an ERR
// trap, an exec hash ran), and reports one "exiting" for errexit, a fatal
// error and an expansion error alike; hash tells them apart here and
// nowhere else.
func (e *Executor) lineEnd(ctx context.Context, lr *lineRun, runErr error) (end LineEnd, status int) {
	var exit interp.ExitStatus
	switch {
	case lr.exitReq != nil:
		return LineExited, lr.exitReq.code
	case errors.Is(context.Cause(ctx), ErrLineInterrupted):
		// The interpreter stops as on a fatal error: the line ended, not
		// the shell.
		return LineInterrupted, 128 + int(syscall.SIGINT)
	case runErr != nil && !errors.As(runErr, &exit):
		// A handler's error, or the line's context ended (the shell's end).
		if sig, ok := SignalCause(ctx); ok {
			return LineFailed, 128 + int(sig)
		}
		return LineFailed, 1
	case !e.runner.Exited():
		return LineReturned, exitCodeFromError(runErr)
	case runnerErrExit(e.runner):
		// errexit, which ends the shell with no call hash could take as a
		// request.
		return LineExited, exitCodeFromError(runErr)
	}
	// Exiting with no exit request, errexit or error: an expansion error,
	// which the interpreter printed.
	return LineFailed, e.expansionErrorStatus()
}
