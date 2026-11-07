package shell

import (
	"context"
	"errors"

	"github.com/tfcace/hash/internal/executor"
)

// errLineInterrupted is the cause a line's context ends with on Ctrl+C,
// which the line's background jobs outlive (the executor spares them for it).
var errLineInterrupted = executor.ErrLineInterrupted

// errLineOver is the cause a line's context ends with once the line is
// over: it returned with no job running, or its last job ended.
var errLineOver = errors.New("line over")

// lineContext is the context a line runs on, with its lifetime. It is not
// a child of the shell's: a background job the line starts runs on it and
// must outlive the line, as in bash. It is
//
//   - running while the line runs;
//   - kept once the line returned with a background job of its own still
//     running, until the last one ends (jobsDone closes);
//   - ended: the line returned with no job running, its last job ended,
//     Ctrl+C ended it (interrupt) or the shell ended, from any state.
//
// Ended is final, and nothing of an ended line is kept: it no longer
// follows the shell's context. The cause says what ended it: Ctrl+C
// (errLineInterrupted), the line being over (errLineOver), else the
// shell's ending (its cause).
type lineContext struct {
	context.Context
	cancel        context.CancelCauseFunc
	stopFollowing func() bool // the shell's AfterFunc that ends the line with the shell
}

// newLineContext is a running line of the shell whose context is shell.
func newLineContext(shell context.Context) *lineContext {
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(shell))
	return &lineContext{
		Context:       ctx,
		cancel:        cancel,
		stopFollowing: context.AfterFunc(shell, func() { cancel(context.Cause(shell)) }),
	}
}

// interrupt ends the line for Ctrl+C.
func (l *lineContext) interrupt() { l.end(errLineInterrupted) }

// interrupted reports that Ctrl+C ended the line.
func (l *lineContext) interrupted() bool {
	return errors.Is(context.Cause(l), errLineInterrupted)
}

// shellEnded reports that the shell's ending ended the line.
func (l *lineContext) shellEnded() bool {
	cause := context.Cause(l)
	return cause != nil && !errors.Is(cause, errLineInterrupted) && !errors.Is(cause, errLineOver)
}

// status is the status of the line that returned with result: the
// result's, final for every way the executor saw the line end (130 for
// Ctrl+C), unless the shell's ending cut the line off: then it ends as a
// line a signal ended (shellEndStatus), whatever it ran. (The interpreter
// says 1 for a context it saw end.) A line that returned as the shell
// ended counts as cut off.
func (l *lineContext) status(result *executor.Result) int {
	if l.shellEnded() {
		return shellEndStatus(l)
	}
	return result.ExitCode
}

// shellEndStatus is the status of a line the shell's ending cut off, ctx
// being the shell's context or one that ended with it: 128 plus the
// signal the executor stops a command with then (StopSignal).
func shellEndStatus(ctx context.Context) int {
	return 128 + int(executor.StopSignal(ctx))
}

// returned is the line returning. jobsDone is closed when the last
// background job the line started ends, nil when it started none: the line
// ends now, or is kept until then. A line that ended already (Ctrl+C cut
// its jobs off with it, or the shell ended) stays ended.
func (l *lineContext) returned(jobsDone <-chan struct{}) {
	switch {
	case jobsDone == nil || l.Err() != nil:
		l.end(errLineOver)
	default:
		go func() {
			select {
			case <-jobsDone:
				l.end(errLineOver)
			case <-l.Done(): // the shell ended it
			}
		}()
	}
}

// end ends the line with cause, from any state.
func (l *lineContext) end(cause error) {
	l.stopFollowing()
	l.cancel(cause)
}

// lineJobsDone is the executor's word on the background jobs of a line that
// returned with result: nil when the line started none, else a channel
// closed when the last one ends.
func lineJobsDone(result *executor.Result) <-chan struct{} {
	if result == nil {
		return nil
	}
	return result.JobsDone
}
