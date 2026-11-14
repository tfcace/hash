package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
)

// lineRun is the state of one Execute call. Handlers find it in their
// context, which a background job inherits from the line that started it:
// a job that outlives its line reports to that finished line, never to a
// later one.
type lineRun struct {
	kind   lineKind
	status *cmdStatus
	output *lineOutput
	// outTerm, errTerm: the terminal the line's stdout/stderr writers
	// reach, nil when one is not the terminal.
	outTerm, errTerm *os.File

	// report draws a refusal for the user, in place of a plain message;
	// nil prints the message to the command's stderr.
	report func(err error)
	// interrupt ends the line for a Ctrl+C (ErrLineInterrupted) only the
	// executor sees: the death of a command on a PTY of its own.
	interrupt func()
	// stopWork ends the line's own work for its exit request (requestExit):
	// the interpreter runs no further statement of it.
	stopWork func()
	// jobsFollow is the context the line's background jobs follow (detach):
	// its caller's. The line's own work runs on a child of it, which the
	// executor ends for a Ctrl+C only it sees and for the shell's exit, and
	// which the jobs outlive.
	jobsFollow context.Context

	// exitReq is the shell's own exit the line requested, nil while it runs
	// on: lineEnd takes it as the line's end. Set and read on the line's
	// goroutine only.
	exitReq *shellExit

	// unread is what the line's PTY commands left unread of the keys
	// typed while they ran: the next PTY command's input first, else the
	// next prompt's (TakeUnread). Its commands run on a PTY one at a
	// time.
	unread []byte
	// ranPTY: a command of the line ran on a PTY (Result.UsedPTY).
	ranPTY bool
	// uncaptured: a command of the line wrote straight to the terminal
	// (writesToTerminal), past the capture (Result.Uncaptured). Atomic: a
	// background job of the line may run one beside the foreground.
	uncaptured atomic.Bool

	mu       sync.Mutex
	refusals []error // commands the line declined to run, in order

	jobs lineJobs // its background jobs, for Result.JobsDone
}

// lineKind is where a line comes from.
type lineKind int

const (
	lineUser    lineKind = iota // a line the user runs, a script's command
	lineStartup                 // a startup file: syntax errors in its evals and sources are skipped
)

type lineRunKey struct{}

func withLineRun(ctx context.Context, lr *lineRun) context.Context {
	return context.WithValue(ctx, lineRunKey{}, lr)
}

// lineRunFrom returns the line a handler call belongs to, or nil outside
// Execute (tests driving the runner directly).
func lineRunFrom(ctx context.Context) *lineRun {
	lr, _ := ctx.Value(lineRunKey{}).(*lineRun)
	return lr
}

// shellExit is a line's request that the shell exit with code, made at the
// point the exit takes effect on the shell itself (requestExit).
type shellExit struct{ code int }

// errShellExit is the cause the line's own work ends with once it
// requested the shell's exit: its jobs run on (detach).
var errShellExit = errors.New("shell exit")

// requestExit makes the line end the shell with code: the exit builtin
// runs on the shell's runner, or an exec's command ended (or, in a
// script, could not start). The first request stands. The line runs
// nothing more: its own work ends (stopWork), its jobs run on.
func (lr *lineRun) requestExit(code int) {
	if lr.exitReq == nil {
		lr.exitReq = &shellExit{code: code}
	}
	if lr.stopWork != nil {
		lr.stopWork()
	}
}

// takeUnread takes the keys an earlier PTY command of the line left
// unread, for the next one.
func (lr *lineRun) takeUnread() []byte {
	if lr == nil {
		return nil
	}
	left := lr.unread
	lr.unread = nil
	return left
}

// keepUnread keeps the keys a PTY command left unread.
func (lr *lineRun) keepUnread(left []byte) {
	if lr != nil {
		lr.unread = append(lr.unread, left...)
	}
}

// refuse reports a command the line declined to run, whose stderr is
// stderr. One whose stderr is the line's own is the line's refusal: it is
// recorded, and drawn for the user (report) when the line has a drawer.
// Any other gets the plain message on its stderr, as bash's, which a
// 2>/dev/null silences (`x=$(nosuch 2>/dev/null)` in a startup file).
func (lr *lineRun) refuse(stderr io.Writer, err error) {
	if stderr != lr.output.stderr {
		fmt.Fprintf(stderr, "hash: %v\n", err)
		return
	}
	lr.mu.Lock()
	lr.refusals = append(lr.refusals, err)
	lr.mu.Unlock()
	if lr.report != nil {
		lr.report(err)
		return
	}
	fmt.Fprintf(stderr, "hash: %v\n", err)
}

func (lr *lineRun) takeRefusals() []error {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	out := lr.refusals
	lr.refusals = nil
	return out
}

// lineOutput is a line's stdout and stderr as the runner sees them. A
// background job keeps the writers of the line that started it, so the job
// and the foreground write through one lock. While the line is live the
// writers are the line's own (the caller's and the capture); end points both
// at the terminal for good, before the line reads its capture, so a job that
// outlives its line never writes into a finished line's writers or a later
// line.
type lineOutput struct {
	mu       sync.Mutex
	state    lineOutputState
	out, err io.Writer // the line's writers while live, the terminal once ended
	// outFile, errFile are the line's writers when each is a file with
	// nothing teed off it (no capture), else nil. A foreground command
	// gets the file itself (commandWriter).
	outFile, errFile *os.File

	stdout, stderr io.Writer // what the runner writes to
}

// lineOutputState is whether a line still owns its writers.
type lineOutputState int

const (
	lineOutputLive  lineOutputState = iota // writes reach the line's writers
	lineOutputEnded                        // the line ended: writes reach the terminal
)

func newLineOutput(out, err io.Writer) *lineOutput {
	o := &lineOutput{out: out, err: err}
	o.outFile, _ = out.(*os.File)
	o.errFile, _ = err.(*os.File)
	o.stdout = lineStream{o: o}
	o.stderr = lineStream{o: o, isErr: true}
	return o
}

// end ends the line: later writes go to the terminal. Once it returns
// nothing writes to the line's writers any more, so they can be read.
func (o *lineOutput) end() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.state == lineOutputEnded {
		return
	}
	o.state = lineOutputEnded
	o.out, o.err = os.Stdout, os.Stderr
}

// commandWriter is the writer for a foreground command whose stream the
// interpreter set to w: the line's own file when w is one of the line's
// streams and the line writes straight to a file, as bash hands a command
// its own descriptors. os/exec puts a pipe between a command and any other
// writer, so a terminal would stop being one (`[ -t 1 ]`, htop, an editor).
func (o *lineOutput) commandWriter(w io.Writer) io.Writer {
	switch {
	case w == o.stdout && o.outFile != nil:
		return o.outFile
	case w == o.stderr && o.errFile != nil:
		return o.errFile
	}
	return w
}

// jobWriter is the writer for a background job's command whose stream the
// interpreter set to w: the terminal itself when w is one of the line's
// streams and reaches the terminal, as a bash job writes to it. The job
// outlives its line, whose capture it has no business in, and may outlive
// hash, whose pipe to the terminal would end under it (SIGPIPE). Any other
// stream is as a foreground command's (commandWriter).
func (lr *lineRun) jobWriter(w io.Writer) io.Writer {
	switch {
	case w == lr.output.stdout && lr.outTerm != nil:
		return lr.outTerm
	case w == lr.output.stderr && lr.errTerm != nil:
		return lr.errTerm
	}
	return lr.output.commandWriter(w)
}

// lineStream is one of a lineOutput's two streams.
type lineStream struct {
	o     *lineOutput
	isErr bool
}

func (s lineStream) Write(p []byte) (int, error) {
	s.o.mu.Lock()
	defer s.o.mu.Unlock()
	if s.isErr {
		return s.o.err.Write(p)
	}
	return s.o.out.Write(p)
}

// inBackgroundJob reports whether a handler call in ctx runs inside a
// background job: on the context of a job's own part (jobTable.detach).
// Nothing the job runs can see or change that.
//
// Limits: hash makes a job of the `&` statements it parses, which are the
// line, eval, source and alias bodies. Code the interpreter parses on its
// own is not: `builtin eval '...'`, `command eval '...'` and trap strings,
// so a job started there runs like the foreground (it may get a PTY).
func inBackgroundJob(ctx context.Context) bool {
	return jobFrom(ctx) != nil
}

// detachedStdin is a background job's stdin: nothing (/dev/null) when it
// is the shell's own stdin and that is the terminal, as bash gives a job
// with no job control, so it does not steal the prompt's keystrokes; else
// what the job redirected it to, a terminal it names too (`cmd < /dev/tty
// &`).
func (e *Executor) detachedStdin(stdin io.Reader) io.Reader {
	if f, ok := stdin.(*os.File); ok && f == e.input() && isTerminal(f) {
		return nil
	}
	return stdin
}
