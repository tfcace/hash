package executor

import (
	"errors"
	"os"

	"github.com/tfcace/hash/internal/executor/jobexec"
)

// startError is a command whose exec failed: it never ran.
type startError struct{ err error }

func (e *startError) Error() string { return e.err.Error() }
func (e *startError) Unwrap() error { return e.err }

// isExecFailure reports whether err is the failure of a command's exec
// (os.StartProcess), not of what hash set up around it (a PTY).
func isExecFailure(err error) bool {
	var pe *os.PathError
	return errors.As(err, &pe) && pe.Op == "fork/exec"
}

// startFailure is what a start error of the command name (as typed) at
// path means. script is true for a file with no shebang that is not a
// binary: it runs as a sh script instead (jobexec.NoShebangShell).
// Otherwise refusal is the *CommandNotExecutableError to report, in bash's
// words, with status 126; nil when err is no exec failure (hash's own
// error, which stays fatal).
func startFailure(name, path string, err error) (refusal error, script bool) {
	reason, script := jobexec.Failure(path, err)
	if reason == "" {
		return nil, script
	}
	return &CommandNotExecutableError{Command: name, Reason: reason}, false
}
