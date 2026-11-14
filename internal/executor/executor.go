package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tfcace/hash/internal/executor/jobexec"
	"github.com/tfcace/hash/internal/programstatus"
	"github.com/tfcace/hash/internal/progress"
	"github.com/tfcace/hash/internal/shparse"
	"github.com/tfcace/hash/internal/trace"
	"github.com/tfcace/hash/internal/version"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// defaultCaptureSize limits how much output we capture by default (1MB).
const defaultCaptureSize = 1024 * 1024

// CommandNotFoundError is returned when a command doesn't exist in PATH.
type CommandNotFoundError struct {
	Command string
}

func (e *CommandNotFoundError) Error() string {
	return fmt.Sprintf("%s: command not found", e.Command)
}

// CommandNotExecutableError is returned when the command names a file that
// exists but cannot be run: no execute bit, or a directory.
type CommandNotExecutableError struct {
	Command string
	Reason  string // "Permission denied" or "Is a directory", worded as bash does
}

func (e *CommandNotExecutableError) Error() string {
	return fmt.Sprintf("%s: %s", e.Command, e.Reason)
}

// lookupFailure explains why looking up a command failed. A name with a path
// separator that resolves to an existing file or directory is reported as
// not executable, with bash's wording and exit status; anything else is
// command not found.
func lookupFailure(dir, name string) error {
	if strings.Contains(name, "/") {
		path := name
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		if info, err := os.Stat(path); err == nil {
			if info.IsDir() {
				return &CommandNotExecutableError{Command: name, Reason: "Is a directory"}
			}
			if info.Mode()&0o111 == 0 {
				return &CommandNotExecutableError{Command: name, Reason: "Permission denied"}
			}
		}
	}
	return &CommandNotFoundError{Command: name}
}

// IsCommandNotFound checks if an error is a CommandNotFoundError.
func IsCommandNotFound(err error) bool {
	var cnf *CommandNotFoundError
	return errors.As(err, &cnf)
}

// limitedWriter wraps a writer and stops writing after n bytes.
type limitedWriter struct {
	w         io.Writer
	n         int64
	limit     int64 // Original limit
	truncated bool  // Whether truncation occurred
	original  int64 // Total bytes attempted (before truncation)
}

func newLimitedWriter(w io.Writer, limit int64) *limitedWriter {
	return &limitedWriter{w: w, n: limit, limit: limit}
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	l.original += int64(len(p))

	if l.n <= 0 {
		l.truncated = true
		return len(p), nil
	}
	origLen := len(p)
	if int64(origLen) > l.n {
		p = p[:l.n]
		l.truncated = true
	}
	n, err := l.w.Write(p)
	if n > 0 {
		l.n -= int64(n)
	}
	if err != nil {
		return n, err
	}
	if origLen > n {
		return origLen, nil
	}
	return n, nil
}

// WasTruncated returns true if output was truncated.
func (l *limitedWriter) WasTruncated() bool {
	return l.truncated
}

// OriginalSize returns the total bytes attempted before truncation.
func (l *limitedWriter) OriginalSize() int64 {
	return l.original
}

// LimitSize returns the configured limit.
func (l *limitedWriter) LimitSize() int64 {
	return l.limit
}

const (
	ctrlCByte = 0x03
	// doubleCtrlCWait: a second Ctrl+C this soon after the first sends a
	// PTY command's group SIGINT itself, for a program that ignores its
	// terminal's.
	doubleCtrlCWait = 750 * time.Millisecond
	// ptyStopGrace is how long a PTY command whose output ended has to
	// exit before its group gets SIGTERM, and how long the PTY of one that
	// exited may be quiet before its output copy stops (outputCopy).
	ptyStopGrace = 200 * time.Millisecond
	// ptyDrainCap is how long the output copy of a PTY command that exited
	// goes on at the most: a child it left may write to the PTY forever.
	ptyDrainCap  = 2 * time.Second
	ptyTraceTick = 5 * time.Second
)

type ptyTrace struct {
	path         string
	f            *os.File
	stop         chan struct{} // closed by Close: the monitor stops
	stopped      chan struct{} // closed once it did
	mu           sync.Mutex
	lastInRead   int64
	lastInWrite  int64
	lastOutRead  int64
	lastOutWrite int64
}

func ptyTraceEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("HASH_PTY_TRACE")))
	if v == "" || v == "0" || v == "false" || v == "no" {
		return false
	}
	return true
}

// ptyTracePath is where HASH_PTY_TRACE writes: HASH_PTY_TRACE_PATH, else
// a file of the user's in the temporary directory (not the working
// directory, which may be a repository or shared).
func ptyTracePath() string {
	if path := strings.TrimSpace(os.Getenv("HASH_PTY_TRACE_PATH")); path != "" {
		return path
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("hash-pty-trace-%d.log", os.Getuid()))
}

func newPTYTrace(cmd *exec.Cmd, ptmx *os.File) *ptyTrace {
	if !ptyTraceEnabled() {
		return nil
	}

	path := ptyTracePath()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil //nolint:nilerr // graceful degradation: skip tracing if file can't be opened
	}

	t := &ptyTrace{path: path, f: f, stop: make(chan struct{}), stopped: make(chan struct{})}
	pid := 0
	if cmd != nil && cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	cmdPath := ""
	args := []string(nil)
	if cmd != nil {
		cmdPath = cmd.Path
		args = cmd.Args
	}
	ptyName := ""
	if ptmx != nil {
		ptyName = ptmx.Name()
	}
	t.logf("=== pty trace start pid=%d cmd=%q args=%q pty=%q path=%q", pid, cmdPath, args, ptyName, path)
	go t.monitor()
	return t
}

func (t *ptyTrace) Close() {
	if t == nil {
		return
	}
	close(t.stop)
	<-t.stopped
	t.logStatus("trace end")
	t.logf("=== pty trace end")
	_ = t.f.Close()
}

func (t *ptyTrace) logf(format string, args ...interface{}) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	ts := time.Now().Format(time.RFC3339Nano)
	fmt.Fprintf(t.f, "%s %s\n", ts, fmt.Sprintf(format, args...))
}

func (t *ptyTrace) formatTime(ns int64) string {
	if ns == 0 {
		return "never"
	}
	ts := time.Unix(0, ns)
	age := time.Since(ts).Round(time.Millisecond)
	return fmt.Sprintf("%s (%s ago)", ts.Format(time.RFC3339Nano), age)
}

func (t *ptyTrace) logStatus(prefix string) {
	if t == nil {
		return
	}
	t.logf("%s in_read=%s in_write=%s out_read=%s out_write=%s", prefix,
		t.formatTime(atomic.LoadInt64(&t.lastInRead)),
		t.formatTime(atomic.LoadInt64(&t.lastInWrite)),
		t.formatTime(atomic.LoadInt64(&t.lastOutRead)),
		t.formatTime(atomic.LoadInt64(&t.lastOutWrite)),
	)
}

// monitor logs the copies' last reads and writes every ptyTraceTick, until
// Close.
func (t *ptyTrace) monitor() {
	defer close(t.stopped)
	ticker := time.NewTicker(ptyTraceTick)
	defer ticker.Stop()
	for {
		select {
		case <-t.stop:
			t.logStatus("trace stop")
			return
		case <-ticker.C:
			t.logStatus("trace tick")
		}
	}
}

func (t *ptyTrace) markInRead(n int) {
	if t == nil || n == 0 {
		return
	}
	atomic.StoreInt64(&t.lastInRead, time.Now().UnixNano())
}

func (t *ptyTrace) markInWrite(n int) {
	if t == nil || n == 0 {
		return
	}
	atomic.StoreInt64(&t.lastInWrite, time.Now().UnixNano())
}

func (t *ptyTrace) markOutRead(n int) {
	if t == nil || n == 0 {
		return
	}
	atomic.StoreInt64(&t.lastOutRead, time.Now().UnixNano())
}

func (t *ptyTrace) markOutWrite(n int) {
	if t == nil || n == 0 {
		return
	}
	atomic.StoreInt64(&t.lastOutWrite, time.Now().UnixNano())
}

func describeCopyEnd(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, errCopyDone):
		return "done"
	case errors.Is(err, io.EOF):
		return "eof"
	case errors.Is(err, os.ErrClosed):
		return "closed"
	case errors.Is(err, syscall.EIO):
		return "eio"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return err.Error()
	}
}

func isRetryableIO(err error) bool {
	if err == nil {
		return false
	}
	if os.IsTimeout(err) {
		return true
	}
	if errors.Is(err, syscall.EINTR) {
		return true
	}
	if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
		return true
	}
	return false
}

func writeAll(dst io.Writer, buf []byte) error {
	for len(buf) > 0 {
		n, err := dst.Write(buf)
		if n > 0 {
			buf = buf[n:]
		}
		if err != nil {
			if isRetryableIO(err) {
				continue
			}
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

// hasDataAvailable reports whether fd has data to read within timeout. A
// select a signal interrupts (EINTR: Go preempts goroutines with SIGURG)
// goes on for the rest of the time.
func hasDataAvailable(fd int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		var readSet unix.FdSet
		readSet.Set(fd)
		tv := unix.NsecToTimeval(max(time.Until(deadline), 0).Nanoseconds())
		_, err := unix.Select(fd+1, &readSet, nil, nil, &tv)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		return err == nil && readSet.IsSet(fd)
	}
}

// terminalReplyWait bounds the wait for the replies to terminal queries
// when a PTY command starts: a terminal that does not answer the fence
// query (fenceQuery), or a stdin hash cannot write it to, costs every PTY
// command this much. A variable for tests.
var terminalReplyWait = 50 * time.Millisecond

// fenceQuery asks the terminal for a status report (DSR 5), which every
// terminal answers (fenceReply), and in order: once its reply is in, so
// are the replies to every query sent before it. Programs and libraries
// rarely ask for it, so a reply to one of theirs still pending is unlikely
// to be taken for the fence's, as a device attributes reply was (a common
// query, which hash used before).
const fenceQuery = "\x1b[5n"

// fenceReply is the terminal's answer to fenceQuery: "OK".
const fenceReply = "\x1b[0n"

// drainTerminalReplies reads what is pending on the terminal at fd before
// a PTY command starts and returns the keys in it, without the replies to
// terminal queries. Libraries like charmbracelet/colorprofile query the
// terminal (DECRQSS, colors, device attributes) and the replies arrive a
// moment later; read by the command they would show as garbage input. The
// read ends once the terminal answered fenceQuery, after them, or within
// terminalReplyWait. Keys typed ahead of the command are the command's, as
// in any shell: the caller copies them to it.
func drainTerminalReplies(fd int, ptyTr *ptyTrace) []byte {
	_, err := syscall.Write(fd, []byte(fenceQuery))
	fenced := err == nil
	deadline := time.Now().Add(terminalReplyWait)
	var pending []byte
	buf := make([]byte, 512)
	for {
		wait := max(time.Until(deadline), 0)
		switch {
		case fenced && bytes.Contains(pending, []byte(fenceReply)):
			wait = 0 // the fence is in: take what came with it
		case !fenced && len(pending) > 0:
			wait = 5 * time.Millisecond // no fence: a pause ends the replies
		}
		if !hasDataAvailable(fd, wait) {
			break
		}
		n, err := syscall.Read(fd, buf)
		if err != nil || n <= 0 {
			break
		}
		pending = append(pending, buf[:n]...)
	}
	keys := splitTerminalReplies(pending)
	if ptyTr != nil {
		ptyTr.logf("drain: fenced %v, read %d bytes, kept %d of keys", fenced, len(pending), len(keys))
	}
	return keys
}

// splitTerminalReplies returns in without the replies a terminal sends to
// queries: device control, operating system command and other strings
// (DECRQSS, colors), and CSI replies, the private ones (CSI ?, >, = as in
// device attributes and keyboard flags) and cursor position and status
// reports (final R, n). Every other byte is a key the user typed, arrows
// and lone escapes included, and so is a sequence cut short.
func splitTerminalReplies(in []byte) []byte {
	var keys []byte
	for i := 0; i < len(in); {
		if in[i] != 0x1b || i+1 >= len(in) {
			keys = append(keys, in[i])
			i++
			continue
		}
		n, reply := terminalReply(in[i:])
		if !reply {
			n = 1
		}
		if n == 0 { // cut short: the rest is the user's
			return append(keys, in[i:]...)
		}
		if !reply {
			keys = append(keys, in[i])
		}
		i += n
	}
	return keys
}

// terminalReply reads the escape sequence seq starts with: its length, 0
// when seq ends before it does, and whether it is a reply to a query.
func terminalReply(seq []byte) (n int, reply bool) {
	switch seq[1] {
	case '[':
		return csiReply(seq)
	case ']', 'P', '_', '^', 'X':
		return stringEnd(seq), true
	}
	return 1, false
}

// csiReply reads the CSI sequence seq starts with (see terminalReply).
func csiReply(seq []byte) (n int, reply bool) {
	for j := 2; j < len(seq); j++ {
		if c := seq[j]; c >= 0x40 && c <= 0x7e {
			private := j > 2 && (seq[2] == '?' || seq[2] == '>' || seq[2] == '=')
			return j + 1, private || c == 'R' || c == 'n'
		}
	}
	return 0, true
}

// stringEnd is the length of the control string (OSC, DCS, ...) seq
// starts with: up to ST, or BEL for OSC; 0 when seq ends first.
func stringEnd(seq []byte) int {
	for j := 2; j < len(seq); j++ {
		if seq[j] == 0x07 && seq[1] == ']' {
			return j + 1
		}
		if seq[j] == 0x1b && j+1 < len(seq) && seq[j+1] == '\\' {
			return j + 2
		}
	}
	return 0
}

func signalProcessGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}
	if err := syscall.Kill(-pid, sig); err != nil {
		_ = cmd.Process.Signal(sig)
	}
}

// Result contains the outcome of a command execution.
type Result struct {
	ExitCode       int
	Duration       time.Duration
	Command        string
	CapturedOutput string
	UsedPTY        bool // True if command ran with PTY (TUI apps, interactive programs)
	// Uncaptured: a command of the line wrote straight to the terminal, as
	// a pipeline's tail at the prompt does (`ls | less`): CapturedOutput
	// does not have what it printed.
	Uncaptured bool
	// End is how the line ended; ExitCode is final for each (130 for
	// LineInterrupted), so the caller sets no status of its own.
	End LineEnd
	// JobsDone is nil when no background job the line started (itself, or
	// through a job it started) still runs as it returns; else it is closed
	// when the last of them ends. A job runs on the line's context: the
	// caller must not end that context before then (Ctrl+C, or the shell
	// ending, may).
	JobsDone <-chan struct{}
	// Refusals lists the commands the shell declined to run on this line
	// with the line's own stderr (refuse), in order: *CommandNotFoundError
	// or *CommandNotExecutableError. Each one set its exit status (127 or
	// 126) and the line carried on, as in bash.
	Refusals []error
}

// Executor runs shell commands using mvdan/sh interpreter.
type Executor struct {
	shellName         string
	shellPath         string
	lang              syntax.LangVariant
	progressOSC       *progress.OSC
	progressThreshold time.Duration
	status            *programstatus.Reporter // OSC 7501 reports, to the terminal
	env               *envStore
	positionalArgs    []string // $0, $1, $2, etc. for -c execution
	captureLimit      int64
	terminal          terminalMode // how much of the terminal hash drives; plain until the REPL says

	// Persistent interpreter state - keeps function definitions across
	// executions. nil until the first Execute creates it (in the working
	// directory of that moment); never dropped afterwards.
	runner     *interp.Runner
	builtins   map[string]BuiltinFunc // Hash builtins that run inside the interpreter
	runnerMu   sync.Mutex             // Protects runner access during execution
	sources    pendingSources         // parsed files on their way to the source builtin
	filter     sourceFilter           // what a sourced file's text goes through first
	traps      shellTraps             // the EXIT and signal traps, kept from the interpreter
	jobs       jobTable               // the background jobs running
	stdin      *os.File               // the shell's own input when it is not os.Stdin (input)
	foreground foreground             // the external commands the shell waits for
	self       selfBinary             // hash's own executable as it started, for its jobs (runJob)
	owed       owedTraps              // the signal traps the running line owes
	typeahead  typeahead              // the keys finished lines left unread, the next prompt's
	// dir is the working directory hash's own process is in, as hash last
	// set it (enterDir): the shell's, the runner's Dir, which the shell's
	// cd moves both to at once. A command run in it inherits it
	// (commandDir), a deleted one too.
	dir atomic.Pointer[string]

	// functions is the names of the shell's functions, as its runner had
	// them when the last line ended (Functions); nil before the first.
	functions atomic.Pointer[[]string]

	// refusalHandler reports a refused command (not found, not executable)
	// for the line the user runs; each line takes it into its lineRun.
	refusalHandler func(err error)

	aliases aliasTable // the aliases the shell defined, kept as functions
}

// SetRefusalHandler installs the function that reports a refused command at
// the moment it fails, in place of the executor's plain stderr message. It
// is called only for the foreground of the running line, never for a
// background job.
func (e *Executor) SetRefusalHandler(fn func(err error)) {
	e.refusalHandler = fn
}

// noteRefusal reports a refused command. The foreground's refusals belong to
// the line (Result.Refusals) and go to the line's handler. A background job
// can refuse while a later line runs, so it reports to nobody's line: it
// writes a plain message to its own stderr.
func noteRefusal(ctx context.Context, hc interp.HandlerContext, err error) {
	lr := lineRunFrom(ctx)
	if lr == nil || inBackgroundJob(ctx) {
		fmt.Fprintf(hc.Stderr, "hash: %v\n", err)
		return
	}
	lr.refuse(hc.Stderr, err)
}

// refusalExitStatus is the status a refused command sets: 126 for a path
// that exists but cannot run, 127 for a command that was not found.
func refusalExitStatus(err error) uint8 {
	var nex *CommandNotExecutableError
	if errors.As(err, &nex) {
		return 126
	}
	return 127
}

// New creates a new Executor.
func New() *Executor {
	execPath, _ := os.Executable()
	env := newEnvStoreFromOS()

	e := &Executor{
		shellName:         "hash",
		shellPath:         execPath,
		lang:              syntax.LangBash,
		progressOSC:       progress.NewOSC(os.Stdout),
		progressThreshold: 2 * time.Second,
		env:               env,
		captureLimit:      defaultCaptureSize,
		status:            programstatus.NewReporter(os.Stdout),
		terminal:          terminalPlain,
		self:              binaryAt(execPath),
		// runner is created lazily on first Execute()
	}
	// A plain executor writes no escapes of its own; see terminalMode.
	e.progressOSC.SetEnabled(false)
	e.status.SetEnabled(false)
	if wd, err := os.Getwd(); err == nil {
		e.dir.Store(&wd)
	}
	e.ensureShellEnv()
	e.syncProcessEnv()
	return e
}

// terminalMode is the shell the executor is: whether it is interactive,
// and how much of the terminal it drives.
type terminalMode int

const (
	// terminalPlain is a script or a -c command: commands get hash's stdio
	// as it is, whatever stdin and stdout are, and hash writes no escapes
	// of its own (no PTYs, no OSC 133;C, 7501 or 9;4), as bash.
	terminalPlain terminalMode = iota
	// terminalPlainInteractive is hash -i running a command or a script: an
	// interactive shell (interactive), on the terminal as terminalPlain.
	terminalPlainInteractive
	// terminalInteractive is the interactive shell: a command whose stdout
	// is the terminal runs on a PTY, and hash marks command output (OSC
	// 133;C) and reports long lines (OSC 7501, 9;4) on the terminal.
	terminalInteractive
)

// interactive reports whether the shell is an interactive one, the REPL's
// or hash -i's: its programs see an i in $- and [[ -o interactive ]] hold
// (markInteractive), as bash's and zsh's do.
func (m terminalMode) interactive() bool {
	return m != terminalPlain
}

// UseInteractiveShell makes the executor an interactive shell that drives
// none of the terminal (terminalPlainInteractive): hash -i running a
// command or a script, as bash -i does. The REPL's shell is one already
// (UseInteractiveTerminal).
func (e *Executor) UseInteractiveShell() {
	e.runnerMu.Lock()
	defer e.runnerMu.Unlock()
	if e.terminal == terminalPlain {
		e.terminal = terminalPlainInteractive
	}
}

// UseInteractiveTerminal switches the executor to the interactive shell's
// terminal features (see terminalInteractive). Only the REPL calls it. The
// escapes still go out only when stdout is a terminal: hash | tee must not
// write them into the pipe.
//
// The REPL takes SIGINT for its whole life and routes it to InterruptLine
// for each line, so the executor counts on it (WatchInterrupts).
func (e *Executor) UseInteractiveTerminal() {
	e.WatchInterrupts()
	e.runnerMu.Lock()
	defer e.runnerMu.Unlock()
	e.terminal = terminalInteractive
	e.setInteractivePS1()
	onTerminal := term.IsTerminal(int(os.Stdout.Fd()))
	e.progressOSC.SetEnabled(onTerminal)
	e.status.SetEnabled(onTerminal)
}

// interactivePS1 is the interactive shell's PS1 when none is set, bash's
// default. hash draws its own prompt; PS1 is there for startup files that
// tell an interactive shell by it (`[ -z "$PS1" ] && return`).
const interactivePS1 = `\s-\v\$ `

// setInteractivePS1 gives the interactive shell its PS1, a shell variable
// (commands do not inherit it, as in bash), unless one is set. The caller
// holds runnerMu.
func (e *Executor) setInteractivePS1() {
	ps1 := expand.Variable{Set: true, Kind: expand.String, Str: interactivePS1}
	if e.runner != nil {
		if !e.runner.Vars["PS1"].IsSet() {
			e.runner.Vars["PS1"] = ps1
		}
		return
	}
	if e.env != nil && !e.env.Get("PS1").IsSet() {
		e.env.set("PS1", ps1)
	}
}

func langVariantForDialect(dialect string) (syntax.LangVariant, error) {
	switch strings.ToLower(strings.TrimSpace(dialect)) {
	case "", "bash":
		return syntax.LangBash, nil
	case "zsh":
		return syntax.LangZsh, nil
	default:
		return syntax.LangBash, fmt.Errorf("unsupported shell dialect %q (supported: bash, zsh)", dialect)
	}
}

// SetDialect configures which shell dialect is used when parsing commands.
func (e *Executor) SetDialect(dialect string) error {
	lang, err := langVariantForDialect(dialect)
	if err != nil {
		return err
	}
	e.runnerMu.Lock()
	defer e.runnerMu.Unlock()
	e.lang = lang
	return nil
}

// Dialect returns the configured parser dialect.
func (e *Executor) Dialect() string {
	if e == nil {
		return "bash"
	}
	return e.lang.String()
}

func (e *Executor) newParser(opts ...syntax.ParserOption) *syntax.Parser {
	parserOpts := []syntax.ParserOption{syntax.Variant(e.lang)}
	parserOpts = append(parserOpts, opts...)
	return syntax.NewParser(parserOpts...)
}

// input is the shell's own input, the terminal's keys when it has one.
func (e *Executor) input() *os.File {
	if e.stdin != nil {
		return e.stdin
	}
	return os.Stdin
}

// initRunner creates the persistent interpreter runner.
// Called lazily on first Execute() to allow configuration before first use.
func (e *Executor) initRunner() error {
	opts := []interp.RunnerOption{
		interp.StdIO(e.input(), os.Stdout, os.Stderr), // each line sets its own writers
		interp.Env(e.env),
		interp.CallHandler(e.shellBuiltinHandler),
		interp.ExecHandlers(e.execHandler),
		interp.OpenHandler(e.sources.open),
	}

	runner, err := interp.New(append(opts, positionalParams(e.positionalArgs))...)
	if err != nil {
		return fmt.Errorf("failed to create interpreter: %w", err)
	}
	// Reset now rather than in the first Run: state set before that Run
	// (SetLastStatus) would be wiped.
	runner.Reset()
	e.runner = runner
	return nil
}

// hashHelpText is shown for `hash --help` / `hash -h` from within the Hash
// shell. It is intentionally concise; the full launch-time flag help lives in
// cmd/hash (package main) and is reached via `/usr/local/bin/hash --help`.
const hashHelpText = `Hash (Harness Assisted SHell)

You are already running inside Hash.

  hash version        Show the Hash version
  hash migrate        Import compatible bash/zsh settings
  tips                Show common shortcuts and AI (??) syntax
  status              Show subsystem status

Invoke the agent with ?? (e.g. '?? find large files').
Run '/usr/local/bin/hash --help' for launch flags.
`

// shellBuiltinHandler intercepts source/. and eval commands to parse with the configured dialect.
// This uses CallHandler which runs for ALL commands including builtins, unlike
// ExecHandler which only runs for external commands.
// We handle the command ourselves, then return ":" (no-op) so the Runner
// doesn't also try to run the builtin.
func (e *Executor) shellBuiltinHandler(ctx context.Context, args []string) ([]string, error) {
	if len(args) == 0 {
		return args, nil
	}

	cmd := args[0]
	e.followShellDir(ctx)

	if call, ok := e.cdCall(ctx, args); ok {
		return call, nil
	}

	// Registered Hash builtins write to the interpreter's current stdout and
	// stderr, so they compose with pipelines and redirections. The call is
	// rewritten to ":" or "false" to carry the exit status.
	if fn, ok := e.builtins[cmd]; ok {
		hc := interp.HandlerCtx(ctx)
		if err := fn(ctx, args[1:], hc.Stdout, hc.Stderr); err != nil {
			fmt.Fprintf(hc.Stderr, "hash: %s: %v\n", cmd, err)
			return []string{"false"}, nil
		}
		return []string{":"}, nil
	}

	switch cmd {
	case "source", ".":
		return e.handleSourceCall(ctx, args)

	case "eval":
		return e.handleEvalCall(ctx, args)

	case "complete", "bind":
		// Hash provides completion and key bindings itself, so a shell's
		// completion registrations (NVM's) and key bindings (fzf's,
		// zoxide's) are inert, and quiet.
		return []string{":"}, nil

	case "alias":
		return e.aliasCall(ctx, args[1:]), nil

	case "unalias":
		return e.unaliasCall(ctx, args[1:]), nil

	case "hash":
		return e.handleHashCall(ctx, args)

	case jobStartedCommand:
		return e.jobStarted(ctx, args), nil

	case jobRunCommand:
		return e.runJobPart(ctx, args), nil

	case substBeginCommand:
		return substBegin(ctx), nil

	case substEndCommand:
		return e.substEnd(ctx), nil

	default:
		return e.rewriteBuiltinCall(ctx, args), nil
	}
}

// rewriteBuiltinCall is the call to run for a builtin hash adjusts to act
// as in bash: trap (hash keeps the EXIT and signal traps), kill, exec, a
// bare return or exit, read, wait and shopt. A call of an alias inside its
// own expansion runs the command (expandingAlias). Any other call goes as
// it is.
func (e *Executor) rewriteBuiltinCall(ctx context.Context, args []string) []string {
	switch args[0] {
	case "trap":
		return e.handleTrapCall(ctx, args)
	case "builtin", "command":
		return e.handlePrefixedCall(ctx, args)
	case "kill":
		return e.handleKillCall(ctx, args)
	case "exec":
		return e.handleExecCall(ctx, args)
	case "return":
		return returnCall(ctx, args)
	case "exit":
		return e.exitCall(ctx, e.traps.bareExit(args))
	case "read":
		return readCall(ctx, args, e.jobGroup() == jobexec.OwnGroup)
	case "wait":
		return e.waitCall(ctx, args)
	case "jobs":
		return e.jobsCall(ctx, args)
	case "echo":
		return e.echoCall(ctx, args)
	case "disown":
		return e.disownCall(ctx, args)
	case "shopt":
		return shoptCall(args)
	case "declare", "local", "export", "readonly", "typeset": // no function takes these names
		return e.declarationCall(ctx, args)
	}
	if e.expandingAlias(ctx, args[0]) {
		return append([]string{"command"}, args...)
	}
	return args
}

// echoCall makes echo's output one write, as bash's builtins write theirs:
// the interpreter's echo writes each word and the newline apart, and a
// job writing to the same output beside it could land its own line
// between them. It is a printf of what echo prints. echo -e, whose
// escapes the interpreter expands its own way, and an echo function go as
// they are. It is the printf builtin, not a function named printf.
func (e *Executor) echoCall(ctx context.Context, args []string) []string {
	if _, isFunc := e.callerRunner(ctx).Funcs["echo"]; isFunc {
		return args
	}
	words, newline := args[1:], "\n"
	for len(words) > 0 {
		switch words[0] {
		case "-n":
			newline = ""
		case "-E":
		case "-e":
			return args
		default:
			return []string{"builtin", "printf", "%s", strings.Join(words, " ") + newline}
		}
		words = words[1:]
	}
	return []string{"builtin", "printf", "%s", newline}
}

// declarationCall runs a declaration builtin the interpreter met as a
// plain call (`\local x=1`, `"export" y`, `builtin local`, `$cmd x`): it
// parses only a bare name as a declaration, and has no builtin to call
// for the others ("local: unsupported builtin"). The call is an eval of
// the declaration, its name bare (one the parser takes as a declaration)
// and its arguments quoted as they expanded: split, as bash 5.2 splits
// them where the name is not a bare word. `declare -f NAME...` is hash's
// own: it prints the functions as they were written (functionSourceCall).
func (e *Executor) declarationCall(ctx context.Context, args []string) []string {
	if names, ok := functionQueryNames(args); ok {
		return e.functionSourceCall(ctx, names)
	}
	text := args[0]
	for _, arg := range args[1:] {
		text += " " + shellQuote(arg)
	}
	return []string{"eval", text}
}

// exitCall makes an exit call that ends the shell itself (made by the
// shell's runner, with an exit status the builtin takes) the line's exit
// request (lineRun.requestExit), and hands the call on. The request ends
// the line where the builtin's exit would not: in an ERR trap, whose
// callback the interpreter runs keeping the failed command's status. A
// status that is no number exits with 2, as in bash, where the
// interpreter's exit refuses it and goes on.
func (e *Executor) exitCall(ctx context.Context, args []string) []string {
	hc := interp.HandlerCtx(ctx)
	code := hc.LastExitStatus
	if len(args) == 2 {
		n, err := strconv.Atoi(args[1])
		if err != nil {
			fmt.Fprintf(hc.Stderr, "hash: exit: %s: numeric argument required\n", args[1])
			n, args = 2, []string{args[0], "2"}
		}
		code = n & 0xff
	}
	lr := lineRunFrom(ctx)
	if lr == nil || e.callerRunner(ctx) != e.runner || len(args) > 2 {
		return args
	}
	lr.requestExit(code)
	return args
}

// handlePrefixedCall handles `builtin NAME` and `command NAME` for the
// builtins hash adjusts, past any function of that name as in bash: trap
// and exec as themselves, kill as the kill command, exit as itself, bind
// as nothing. Other calls go as they are.
func (e *Executor) handlePrefixedCall(ctx context.Context, args []string) []string {
	if len(args) < 2 {
		return args
	}
	switch args[1] {
	case "trap":
		return prefixedCall(args[0], e.handleTrapCall(ctx, args[1:]))
	case "exec":
		return prefixedCall(args[0], e.handleExecCall(ctx, args[1:]))
	case "kill":
		return e.killCommand(ctx, args[1:])
	case "exit":
		return prefixedCall(args[0], e.exitCall(ctx, e.traps.bareExit(args[1:])))
	case "wait":
		return prefixedCall(args[0], e.waitCall(ctx, args[1:]))
	case "disown":
		return e.disownCall(ctx, args[1:])
	case "jobs":
		return e.jobsCall(ctx, args[1:])
	case "bind":
		return []string{":"}
	case "declare", "local", "export", "readonly", "typeset":
		return e.declarationCall(ctx, args[1:])
	}
	return args
}

// prefixedCall is call, hash's for a builtin, made through prefix
// (builtin or command): a call of hash's own commands (statusCall, the
// shell's exec) goes as it is, which `builtin` would refuse as no builtin.
func prefixedCall(prefix string, call []string) []string {
	switch call[0] {
	case statusCommand, execCommand:
		return call
	}
	return append([]string{prefix}, call...)
}

// waitCall is a wait call that ends with its line, as in bash, where
// Ctrl+C at wait ends it at once: the interpreter's wait blocks until the
// jobs end (a stopped one's never). It waits for the jobs the call names,
// g1, g2, ... ($!), %N, %%, %- (jobNumber) or the pid jobs -p prints
// (pidJob), or all of the calling shell's, until they ended or ctx did, and then hands the call on, with
// the jobs as gN: the interpreter's wait returns at once with their
// status, and reports a call it does not take (an option, a job that is
// not there) as it would. wait -n is hash's own (waitAny).
func (e *Executor) waitCall(ctx context.Context, args []string) []string {
	caller := e.callerRunner(ctx)
	jobs, ok := runnerJobs(caller)
	if !ok {
		return args
	}
	if len(args) > 1 && args[1] == "-n" {
		return e.waitAny(ctx, args, caller, jobs)
	}
	var waitFor []<-chan struct{}
	if len(args) == 1 {
		waitFor = e.jobs.notDisowned(jobs.all())
		args = statusCall(0) // the interpreter's would wait for the disowned jobs too
	} else {
		ns, bad := e.jobs.named(caller, args[1:], jobs.count())
		if strings.HasPrefix(bad, "-") {
			return args // an option, which the interpreter reports
		}
		if bad != "" {
			return noJobCall(interp.HandlerCtx(ctx).Stderr, bad, false)
		}
		named := args[:1:1]
		for _, n := range ns {
			waitFor = append(waitFor, jobs.done(n))
			named = append(named, "g"+strconv.Itoa(n))
		}
		args = named
	}
	for _, done := range waitFor {
		select {
		case <-done:
		case <-ctx.Done():
			return statusCall(128 + int(StopSignal(ctx)))
		}
	}
	if len(args) > 1 { // it named them: they are waited for
		for _, done := range waitFor {
			e.jobs.waited(done)
		}
	}
	return args
}

// named is the numbers of the jobs ops name among shell's count jobs: a
// job spec (jobNumber), or the pid jobs -p prints for a job that is one
// command (pidJob); bad is the first op that names none.
func (t *jobTable) named(shell *interp.Runner, ops []string, count int) (ns []int, bad string) {
	for _, op := range ops {
		n, found := 0, false
		if isJobSpec(op) {
			n, found = t.jobNumber(shell, op, count)
		} else if pid, err := strconv.Atoi(op); err == nil && pid > 0 {
			n = t.pidJob(shell, pid)
			found = n > 0
		}
		if !found {
			return nil, op
		}
		ns = append(ns, n)
	}
	return ns, ""
}

// noJobCall is the call for wait's operand op that names no job of the
// shell's, its words bash's, on stderr: 127 for a job spec or a pid, 1
// for anything else, which wait -n (waitN) takes for a job spec; 2 for an
// option wait -n does not take.
func noJobCall(stderr io.Writer, op string, waitN bool) []string {
	option := strings.HasPrefix(op, "-")
	switch _, err := strconv.Atoi(op); {
	case isJobSpec(op) || waitN && err != nil && !option:
		fmt.Fprintf(stderr, "hash: wait: %s: no such job\n", op)
	case err == nil:
		fmt.Fprintf(stderr, "hash: wait: pid %s is not a child of this shell\n", op)
	default:
		fmt.Fprintf(stderr, "hash: wait: `%s': not a pid or valid job spec\n", op)
		if option {
			return statusCall(2)
		}
		return statusCall(1)
	}
	return statusCall(127)
}

// waitAny is wait -n, args being the call of caller, whose jobs are jobs:
// it takes the first of the jobs the call names, or of the shell's jobs
// that run or ended and that no wait took (unwaited) when it names none,
// to have ended (the lowest number first of the ones that did), and hands
// on the call that takes that job's status (wait gN), as bash's; 127 when
// there is none to wait for, or an operand names no job (an option, -p,
// is not taken).
func (e *Executor) waitAny(ctx context.Context, args []string, caller *interp.Runner, jobs runnerJobList) []string {
	var ns []int
	if ops := args[2:]; len(ops) > 0 {
		var bad string
		if ns, bad = e.jobs.named(caller, ops, jobs.count()); bad != "" {
			return noJobCall(interp.HandlerCtx(ctx).Stderr, bad, true)
		}
	} else {
		ns = e.jobs.unwaited(caller)
	}
	if len(ns) == 0 {
		return statusCall(127)
	}
	n, ok := firstToEnd(ctx, ns, jobs)
	if !ok {
		return statusCall(128 + int(StopSignal(ctx)))
	}
	e.jobs.waited(jobs.done(n))
	return []string{args[0], "g" + strconv.Itoa(n)}
}

// firstToEnd is the number of the first of the jobs ns number among jobs
// to have ended, the lowest of the ones that ended already; false when
// ctx ended first.
func firstToEnd(ctx context.Context, ns []int, jobs runnerJobList) (int, bool) {
	for _, n := range ns {
		select {
		case <-jobs.done(n):
			return n, true
		default:
		}
	}
	first := make(chan int, 1)
	stop := make(chan struct{})
	defer close(stop)
	for _, n := range ns {
		done := jobs.done(n)
		go func() {
			select {
			case <-done:
				select {
				case first <- n:
				default:
				}
			case <-stop:
			}
		}()
	}
	select {
	case n := <-first:
		return n, true
	case <-ctx.Done():
		return 0, false
	}
}

// returnCall makes a bare return return $?, as in bash; the interpreter's
// returns 0. A return outside a function or a sourced file fails with 2
// and bash's message, where the interpreter's sets 1.
func returnCall(ctx context.Context, args []string) []string {
	hc := interp.HandlerCtx(ctx)
	if in, ok := runnerInFunctionOrSource(handlerRunner(hc)); ok && !in {
		fmt.Fprintln(hc.Stderr, "hash: return: can only `return' from a function or sourced script")
		return statusCall(2)
	}
	if len(args) == 1 {
		return []string{"return", strconv.Itoa(hc.LastExitStatus)}
	}
	return args
}

// hashSubcommands are hash's subcommands that `hash NAME` typed in hash
// runs, as hash's own executable (cmd/hash): hash migrate, hash migrate
// status. hash version and --help are answered here.
var hashSubcommands = map[string]bool{"migrate": true}

// handleHashCall is the call for args, a hash call. The command name
// collides with the POSIX hash builtin, which mvdan/sh stubs as a no-op:
// `hash version` / `--version` / `-v` and `--help` / `-h` report hash's
// own version and help, `hash migrate ...` runs hash's own migrate
// (hashSubcommands), and any other call is the POSIX builtin's
// (hashLookupCall).
func (e *Executor) handleHashCall(ctx context.Context, args []string) ([]string, error) {
	if len(args) < 2 {
		return args, nil
	}
	hc := interp.HandlerCtx(ctx)
	switch {
	case args[1] == "version", args[1] == "--version", args[1] == "-v":
		fmt.Fprintf(hc.Stdout, "hash %s\n", version.String())
		return []string{":"}, nil
	case args[1] == "--help", args[1] == "-h":
		fmt.Fprint(hc.Stdout, hashHelpText)
		return []string{":"}, nil
	case hashSubcommands[args[1]] && e.shellPath != "":
		return append([]string{e.shellPath}, args[1:]...), nil
	}
	return e.hashLookupCall(ctx, args), nil
}

// hashLookupCall is the call for args, a POSIX hash call, which hash has
// no table for (it looks a command up as it runs it): it fails as bash's
// does for a name that is not a builtin, a function or a command in PATH,
// saying so, and does nothing otherwise. A name with a / in it, and every
// name of hash -p, is taken as it is.
func (e *Executor) hashLookupCall(ctx context.Context, args []string) []string {
	hc := interp.HandlerCtx(ctx)
	names := args[1:]
	for len(names) > 0 && len(names[0]) > 1 && names[0][0] == '-' {
		if names[0] == "--" {
			names = names[1:]
			break
		}
		if strings.Contains(names[0], "p") {
			return []string{":"}
		}
		names = names[1:]
	}
	status := 0
	for _, name := range names {
		_, isFunc := e.callerRunner(ctx).Funcs[name]
		_, isHash := e.builtins[name]
		if strings.Contains(name, "/") || isFunc || isHash || interp.IsBuiltin(name) {
			continue
		}
		if _, err := interp.LookPathDir(hc.Dir, hc.Env, name); err != nil {
			fmt.Fprintf(hc.Stderr, "hash: hash: %s: not found\n", name)
			status = 1
		}
	}
	return statusCall(status)
}

// handleSourceCall handles source/. commands with the configured parser dialect.
func (e *Executor) handleSourceCall(ctx context.Context, args []string) ([]string, error) {
	if len(args) < 2 {
		hc := interp.HandlerCtx(ctx)
		fmt.Fprintln(hc.Stderr, "source: need filename")
		return []string{"false"}, nil
	}
	if _, isFunc := e.callerRunner(ctx).Funcs[args[0]]; isFunc {
		return args, nil // a function named source or .: it gets the call as is
	}
	trace.Emit("compat", "source_intercept", trace.LevelVerbose, map[string]any{
		"path": args[1],
	})
	return e.handleSourceWithDialect(ctx, args[1], args[2:]), nil
}

// handleEvalCall handles eval commands with the configured parser dialect.
func (e *Executor) handleEvalCall(ctx context.Context, args []string) ([]string, error) {
	if len(args) < 2 {
		return []string{":"}, nil // eval with no args is a no-op
	}
	trace.Emit("compat", "eval_intercept", trace.LevelVerbose, map[string]any{
		"src_preview": truncateForTrace(strings.Join(args[1:], " "), 200),
	})
	return e.handleEvalWithDialect(ctx, args[1:]), nil
}

// safeRunNode wraps runner.Run() with panic recovery, converting panics to errors.
// This is needed because mvdan/sh can panic on unsupported syntax like [![:space:]].
func safeRunNode(ctx context.Context, runner *interp.Runner, node syntax.Node) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("interpreter panic: %v", r)
		}
	}()
	return runner.Run(ctx, node)
}

// callerRunner returns the runner that made the current handler call: a
// subshell's own runner inside ( ), $( ), a pipeline or an & job. A nested
// program that bash cannot express (runNested) must run there, or a
// subshell's eval would change the parent shell and a substitution's output
// would bypass it. The shell's runner when the interpreter's field is gone
// (see runner_state.go).
func (e *Executor) callerRunner(ctx context.Context) *interp.Runner {
	if r := handlerRunner(interp.HandlerCtx(ctx)); r != nil {
		return r
	}
	return e.runner
}

const unsupportedPromptCommandTrim = `${PROMPT_COMMAND%"${PROMPT_COMMAND##*[![:space:]]}"}`
const zoxideQueryStrictPattern = `result="$(\command zoxide query --exclude "$(__zoxide_pwd)" -- "$@")" &&`
const zoxideQueryCompatPattern = "result=\"$(\\command zoxide query --exclude \"$(__zoxide_pwd)\" -- \"$@\" || \\builtin true)\"\n            result=\"${result%$'\\r'}\"\n            [[ -n \"${result}\" ]] &&"
const zoxideInteractiveStrictPattern = `result="$(\command zoxide query --interactive -- "$@")" && __zoxide_cd "${result}"`
const zoxideInteractiveCompatPattern = "result=\"$(\\command zoxide query --interactive -- \"$@\" || \\builtin true)\"\n    result=\"${result%$'\\r'}\"\n    [[ -n \"${result}\" ]] && __zoxide_cd \"${result}\""
const zoxideQueryStrictLine = `result="$(\command zoxide query --exclude "$(__zoxide_pwd)" -- "$@")"`
const escapedBuiltinLocal = `\builtin local `
const builtinLocal = `builtin local `

// sanitizeUnsupportedExpansions rewrites bash expansions that currently panic in
// mvdan/sh. Keep transformations minimal and syntax-preserving.
func sanitizeUnsupportedExpansions(src string) (sanitized string, changed bool) {
	sanitized = src
	if strings.Contains(sanitized, unsupportedPromptCommandTrim) {
		sanitized = strings.ReplaceAll(sanitized, unsupportedPromptCommandTrim, `${PROMPT_COMMAND}`)
		changed = true
	}
	if strings.Contains(sanitized, escapedBuiltinLocal) {
		sanitized = strings.ReplaceAll(sanitized, escapedBuiltinLocal, `local `)
		changed = true
	}
	if strings.Contains(sanitized, builtinLocal) {
		sanitized = strings.ReplaceAll(sanitized, builtinLocal, `local `)
		changed = true
	}
	if strings.Contains(sanitized, zoxideQueryStrictPattern) {
		sanitized = strings.ReplaceAll(sanitized, zoxideQueryStrictPattern, zoxideQueryCompatPattern)
		changed = true
	} else if strings.Contains(sanitized, zoxideQueryStrictLine) {
		sanitized = strings.ReplaceAll(
			sanitized,
			zoxideQueryStrictLine,
			`result="$(\command zoxide query --exclude "$(__zoxide_pwd)" -- "$@" || \builtin true)"`+"\n            result=\"${result%$'\\r'}\"\n            [[ -n \"${result}\" ]]",
		)
		changed = true
	}
	if strings.Contains(sanitized, zoxideInteractiveStrictPattern) {
		sanitized = strings.ReplaceAll(sanitized, zoxideInteractiveStrictPattern, zoxideInteractiveCompatPattern)
		changed = true
	}
	return sanitized, changed
}

// handleSourceWithDialect reads a file, parses it with the configured
// dialect and returns the call that runs what of it loads (loadSource) in
// the calling shell. A file that does not load all says so (notLoaded) and
// sets status 2, as bash's source of a file with a syntax error.
func (e *Executor) handleSourceWithDialect(ctx context.Context, path string, params []string) []string {
	hc := interp.HandlerCtx(ctx)
	name := path // as the user wrote it, for messages

	// path is as the shell expanded it: an unquoted ~ is the home already,
	// a quoted one is a file named ~, as in bash.
	// Resolve relative paths against current directory
	if !filepath.IsAbs(path) {
		path = filepath.Join(hc.Dir, path)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(hc.Stderr, "source: %v\n", err)
		return []string{"false"}
	}
	startupRunFrom(ctx).read(path)
	sf, prog := e.loadSource(path, content, startupRunFrom(ctx))
	switch sf.Load {
	case LoadedAll:
		trace.Emit("compat", "source_execute", trace.LevelVerbose, map[string]any{
			"path": path,
		})
		return e.sourcedCall(ctx, path, prog, params)
	case LoadedBefore:
		e.notLoaded(ctx, name, path, sf)
		// The status of a syntax error, set as the file's text can: the
		// status command's name does not survive being printed.
		status, _ := syntax.NewParser().Parse(strings.NewReader("(exit 2)\n"), "")
		prog.Stmts = append(prog.Stmts, status.Stmts...)
		return e.sourcedCall(ctx, path, prog, params)
	}
	e.notLoaded(ctx, name, path, sf)
	return statusCall(2)
}

// sourcedCall is the call that runs prog, the file at path that source
// reads, in the calling shell, with params its positional parameters.
func (e *Executor) sourcedCall(ctx context.Context, path string, prog *syntax.File, params []string) []string {
	e.prepare(prog)
	if text, ok := e.bashText(prog); ok {
		return append([]string{"source", e.sources.put(path, text)}, params...)
	}
	return runNested(ctx, e.callerRunner(ctx), prog, path)
}

// handleEvalWithDialect parses args with the configured parser dialect and
// returns the call that runs them in the calling shell.
func (e *Executor) handleEvalWithDialect(ctx context.Context, args []string) []string {
	src := strings.Join(args, " ")
	parsedSrc, sanitized := sanitizeUnsupportedExpansions(src)
	if sanitized {
		trace.Emit("compat", "eval_sanitize", trace.LevelVerbose, map[string]any{
			"src_preview": truncateForTrace(src, 200),
		})
	}

	parser := e.newParser()
	prog, err := shparse.Parse(parser, parsedSrc, "eval")
	if err != nil {
		trace.Emit("compat", "eval_parse_error", trace.LevelVerbose, map[string]any{
			"src_preview": truncateForTrace(src, 200),
			"error":       err.Error(),
		})
		return syntaxErrorCall(ctx, "eval", err)
	}

	e.prepare(prog)
	trace.Emit("compat", "eval_execute", trace.LevelVerbose, map[string]any{
		"src_preview": truncateForTrace(src, 100),
	})
	if text, ok := e.bashText(prog); ok {
		return []string{"eval", text}
	}
	return runNested(ctx, e.callerRunner(ctx), prog, "eval")
}

// syntaxErrorCall reports a syntax error in an eval or a job's text, named
// name, and returns the call that sets status 2, as bash. In a startup file
// it is skipped quietly instead (see ExecuteStartup).
func syntaxErrorCall(ctx context.Context, name string, err error) []string {
	if lr := lineRunFrom(ctx); lr != nil && lr.kind == lineStartup {
		return []string{":"}
	}
	line, msg := SyntaxError(err)
	fmt.Fprintf(interp.HandlerCtx(ctx).Stderr, "hash: %s: line %d: %s\n", name, line, msg)
	return statusCall(2)
}

// SyntaxError splits a parse error into the line it is on (1 when it has
// no position) and its message without the position.
func SyntaxError(err error) (line uint, msg string) {
	var pos syntax.Pos
	msg = err.Error()
	var parseErr syntax.ParseError
	var langErr syntax.LangError
	switch {
	case errors.As(err, &parseErr):
		pos, msg = parseErr.Pos, parseErr.Text
	case errors.As(err, &langErr):
		where := langErr.Pos.String() + ": "
		if langErr.Filename != "" {
			where = langErr.Filename + ":" + where
		}
		pos, msg = langErr.Pos, strings.TrimPrefix(msg, where)
	}
	return max(pos.Line(), 1), msg
}

// statusCommand is a command name only hash's exec handler answers: it sets
// the status its argument names. A call handler returns statusCall to give
// a call any status, where the builtins it could pick from set only 0 or 1.
// The NUL keeps it out of reach of a command a user can type.
const statusCommand = "\x00hash-status"

// statusCall is the call that sets the status code.
func statusCall(code int) []string {
	if code == 0 {
		return []string{":"}
	}
	return []string{statusCommand, strconv.Itoa(code)}
}

// runStatementsWithRecovery executes a program's statements one at a time,
// skipping any that panic. This allows function definitions to survive even
// when other statements (like zoxide's PROMPT_COMMAND manipulation) crash.
func runStatementsWithRecovery(ctx context.Context, runner *interp.Runner, prog *syntax.File, source string) error {
	var lastErr error
	for i, stmt := range prog.Stmts {
		if err := safeRunNode(ctx, runner, stmt); err != nil {
			if strings.HasPrefix(err.Error(), "interpreter panic:") {
				trace.Emit("compat", "stmt_panic_skip", trace.LevelVerbose, map[string]any{
					"source":    source,
					"statement": i + 1,
					"error":     err.Error(),
				})
				continue
			}
			lastErr = err
		}
	}
	return lastErr
}

// truncateForTrace truncates a string for trace output.
func truncateForTrace(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// SetCaptureLimit sets the maximum number of bytes to capture.
// Use a negative value for unlimited capture, or 0 to disable capture.
func (e *Executor) SetCaptureLimit(limit int64) {
	e.captureLimit = limit
}

// SetShellName sets the shell name for $0.
func (e *Executor) SetShellName(name string) {
	e.shellName = name
}

// ShellName returns the shell name.
func (e *Executor) ShellName() string {
	return e.shellName
}

// ShellPath returns the path to the shell executable.
func (e *Executor) ShellPath() string {
	return e.shellPath
}

// Functions returns the names of the shell's functions (and aliases,
// which hash defines as functions), as they were when the last line
// ended, for completion: the runner's own, which a line may change as it
// runs.
func (e *Executor) Functions() []string {
	if names := e.functions.Load(); names != nil {
		return slices.Clone(*names)
	}
	return nil
}

// Environ returns all environment variables in "NAME=value" format.
// This is used for tab-completion of environment variables.
func (e *Executor) Environ() []string {
	if e == nil || e.env == nil {
		return os.Environ()
	}

	var result []string
	e.env.Each(func(name string, vr expand.Variable) bool {
		if vr.Set && vr.Kind == expand.String {
			result = append(result, name+"="+vr.Str)
		}
		return true
	})
	return result
}

// Execute runs a command line using the mvdan/sh interpreter. The traps of
// the signals that did not end the shell run once the line they came
// during ended, or before the next line when they came with none running
// (owedTraps).
func (e *Executor) Execute(ctx context.Context, command string, stdout, stderr io.Writer) (*Result, error) {
	return e.executeLine(ctx, command, stdout, stderr, lineUser)
}

// executeLine runs a line (execute), after the traps of the signals that
// came before it with no line running, and then, while it still counts as
// in flight, the traps of the signals that came during it or during those
// traps (owedTraps).
func (e *Executor) executeLine(ctx context.Context, command string, stdout, stderr io.Writer, kind lineKind) (*Result, error) {
	for _, sig := range e.owed.lineStarts() {
		if !e.runsLineTrap(sig) {
			continue
		}
		if code, exited := e.runOwedTrap(ctx, sig, e.lastStatus(), stdout, stderr); exited {
			e.owed.lineEnds()
			return &Result{ExitCode: code, Command: command, End: LineExited}, nil
		}
	}
	res, err := e.execute(ctx, command, stdout, stderr, kind)
	defer e.owed.lineEnds()
	for owed := e.owed.takeLineOwed(); len(owed) > 0; owed = e.owed.takeLineOwed() {
		for _, sig := range owed {
			if err != nil || res.End == LineExited || !e.runsLineTrap(sig) {
				continue
			}
			if code, exited := e.runOwedTrap(ctx, sig, res.ExitCode, stdout, stderr); exited {
				res.ExitCode, res.End = code, LineExited
			}
		}
	}
	return res, err
}

//nolint:gocyclo // command execution coordinates parsing, capture, and runner lifecycle
func (e *Executor) execute(ctx context.Context, command string, stdout, stderr io.Writer, kind lineKind) (*Result, error) {
	e.runnerMu.Lock()
	defer e.runnerMu.Unlock()

	start := time.Now()
	e.ensureShellEnv()

	// Past the progress threshold the terminal gets the line's program
	// status (OSC 7501) and a progress bar (OSC 9;4); see cmdStatus.
	lr := &lineRun{
		kind:    kind,
		status:  newCmdStatus(e.status, e.progressOSC, startupRunFrom(ctx).shown(command), e.progressThreshold),
		outTerm: terminalOf(stdout),
		errTerm: terminalOf(stderr),
		report:  e.refusalHandler,
	}
	// The line runs on a context of its own, which the executor ends for a
	// Ctrl+C only it sees (runWithPTY) and for the shell's exit; its jobs
	// follow the caller's.
	lr.jobsFollow = withLineRun(ctx, lr)
	ctx, endLine := context.WithCancelCause(lr.jobsFollow)
	lr.interrupt = func() { endLine(ErrLineInterrupted) }
	lr.stopWork = func() { endLine(errShellExit) }
	exitCode := 1
	defer func() {
		lr.status.finish(exitCode)
		if lr.output != nil {
			lr.output.end() // an early return; a line that ran ended it already
		}
	}()

	// Determine $0 value - first positional arg or shell name
	shellName := e.shellName
	if len(e.positionalArgs) > 0 {
		shellName = e.positionalArgs[0]
	}

	// Parse the command with $0 name and the configured shell dialect.
	prog, err := e.lineProgram(command, shellName, kind)
	if err != nil {
		return nil, err
	}

	e.prepare(prog)

	// Set up capture buffer
	var captureBuf bytes.Buffer
	var captureWriter io.Writer
	var lw *limitedWriter
	switch {
	case e.captureLimit == 0:
		captureWriter = io.Discard
	case e.captureLimit > 0:
		lw = newLimitedWriter(&captureBuf, e.captureLimit)
		captureWriter = lw
	default:
		captureWriter = &captureBuf
	}

	// Create persistent runner on first use (lazy init)
	if e.runner == nil {
		if initErr := e.initRunner(); initErr != nil {
			return nil, initErr
		}
	}

	// The line's own writers, for the runner and every job the line starts.
	// With no capture the line writes to stdout itself, which a command
	// then gets as it is (see lineOutput.commandWriter).
	lineStdout := captureWriter
	switch {
	case stdout != nil && captureWriter == io.Discard:
		lineStdout = stdout
	case stdout != nil:
		lineStdout = io.MultiWriter(stdout, captureWriter)
	}
	if stderr == nil {
		stderr = io.Discard
	}
	lr.output = newLineOutput(lineStdout, stderr)
	if ioErr := interp.StdIO(e.input(), lr.output.stdout, lr.output.stderr)(e.runner); ioErr != nil {
		return nil, ioErr
	}

	// Run with persistent runner - functions persist across executions
	err = safeRunNode(ctx, e.runner, prog)
	// The line is over: its background jobs write to the terminal from here,
	// so reading the capture below cannot race them.
	lr.output.end()
	names := slices.Collect(maps.Keys(e.runner.Funcs))
	e.functions.Store(&names)
	e.updateEnvFromRunner(e.runner)
	e.ensureShellEnv()
	e.syncProcessEnv()
	e.syncWorkingDir()

	// Show truncation warning if output was truncated
	if lw != nil && lw.WasTruncated() {
		fmt.Fprintf(os.Stderr, "\033[90m(output truncated: %s → %s for clipboard/agent)\033[0m\n",
			formatBytes(lw.OriginalSize()),
			formatBytes(lw.LimitSize()))
	}

	end, code := e.lineEnd(ctx, lr, err)
	exitCode = code
	e.typeahead.keep(lr.unread)
	return &Result{
		ExitCode:       exitCode,
		Duration:       time.Since(start),
		Command:        command,
		CapturedOutput: captureBuf.String(),
		UsedPTY:        lr.ranPTY,
		Uncaptured:     lr.uncaptured.Load(),
		End:            end,
		JobsDone:       lr.jobs.lineReturned(),
		Refusals:       lr.takeRefusals(),
	}, nil
}

// pipelineContext says which of a command's streams are the terminal.
type pipelineContext struct {
	interactive   bool // the interactive shell, at a terminal (stdin and the line's stdout)
	hcStdinIsTerm bool // The command's stdin is the terminal (not a file or pipe)
	// stdoutTerm is the terminal the command's stdout is (not redirected or
	// piped), nil when it is not the terminal.
	stdoutTerm *os.File
	// stderrTerm is the terminal the command's stderr reaches, nil when it
	// is not the terminal (redirected, piped).
	stderrTerm *os.File
}

// detectPipelineContext works out which of the command's streams are the
// terminal. A redirect or pipe replaces the shell's own writer with a file;
// the shell's writer is the terminal when the line's output is.
func (e *Executor) detectPipelineContext(hc interp.HandlerContext, lr *lineRun) pipelineContext {
	var pc pipelineContext
	if f, ok := hc.Stdin.(*os.File); ok {
		pc.hcStdinIsTerm = isTerminal(f)
	}
	if lr == nil {
		return pc // outside a line nothing is the terminal
	}
	pc.interactive = e.terminal == terminalInteractive && lr.outTerm != nil && term.IsTerminal(int(e.input().Fd()))
	if hc.Stdout == lr.output.stdout {
		pc.stdoutTerm = lr.outTerm
	}
	switch hc.Stderr {
	case lr.output.stderr:
		pc.stderrTerm = lr.errTerm
	case lr.output.stdout: // 2>&1
		pc.stderrTerm = lr.outTerm
	}
	return pc
}

// needsPTY reports whether the command runs on a PTY of its own
// (runWithPTY): in the interactive shell, when its stdin and stdout are
// the terminal. A pipeline gets at most one PTY, for a command that is
// all of it.
func (pc pipelineContext) needsPTY() bool {
	return pc.interactive && pc.stdoutTerm != nil && pc.hcStdinIsTerm
}

// writesToTerminal reports whether the command writes to the interactive
// shell's terminal with its stdin elsewhere: a pipeline's tail (`ls |
// less`), `prog < file`. It gets the terminal itself for its stdout and its
// stderr, as in any shell, uncaptured: the two stay in the order it wrote
// them, a pager or an editor reads its keys from the terminal (/dev/tty,
// or its stderr), and the terminal stays cooked for the pipeline's other
// commands (`stty -a | cat`, a password prompt on /dev/tty). A PTY there
// would need hash to copy the terminal's keys to it, taking them from
// those commands.
func (pc pipelineContext) writesToTerminal() bool {
	return pc.interactive && pc.stdoutTerm != nil && !pc.hcStdinIsTerm
}

// terminalOf is the terminal w is, or wraps (a writer that tees a terminal
// exposes it with Unwrap); nil when w does not reach a terminal.
func terminalOf(w io.Writer) *os.File {
	for w != nil {
		switch v := w.(type) {
		case *os.File:
			if isTerminal(v) {
				return v
			}
			return nil
		case interface{ Unwrap() io.Writer }:
			w = v.Unwrap()
		default:
			return nil
		}
	}
	return nil
}

// execHandler spawns external commands with PTY when needed.
func (e *Executor) execHandler(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		switch args[0] {
		case statusCommand:
			code, err := strconv.Atoi(args[1])
			if err != nil || code < 0 || code > 255 {
				return interp.ExitStatus(2)
			}
			return interp.ExitStatus(uint8(code))
		case execCommand:
			return e.execReplacing(ctx, args[1:])
		}
		return e.runExternal(ctx, args)
	}
}

// runExternal runs the external command args, as its place in the line
// decides (commandRunner). One that is not there or cannot start is
// refused, with 127 or 126, and the line goes on.
func (e *Executor) runExternal(ctx context.Context, args []string) error {
	hc := interp.HandlerCtx(ctx)
	if ok, err := zshSetupCall(hc, args); ok {
		return err
	}
	path, err := interp.LookPathDir(hc.Dir, hc.Env, args[0])
	if err != nil {
		// Report it and set the status; the rest of the line keeps running
		refusal := lookupFailure(hc.Dir, args[0])
		noteRefusal(ctx, hc, refusal)
		return interp.ExitStatus(refusalExitStatus(refusal))
	}
	if refusal := unenterableDir(args[0], e.commandDir(hc.Dir)); refusal != nil {
		noteRefusal(ctx, hc, refusal)
		return interp.ExitStatus(refusalExitStatus(refusal))
	}

	run := e.commandRunner(ctx, hc, args[0])
	err = run(path, args)
	var se *startError
	if errors.As(err, &se) {
		refusal, script := startFailure(args[0], path, se.err)
		switch {
		case script:
			err = run(jobexec.NoShebangShell, append([]string{"sh", jobexec.ScriptName(path, args[0])}, args[1:]...))
		case refusal != nil:
			noteRefusal(ctx, hc, refusal)
			return interp.ExitStatus(refusalExitStatus(refusal))
		}
	}
	return normalizeExternalCommandError(err)
}

// commandRun runs an external command, the program at path with argv (its
// name as typed first, its argv[0], as bash passes it): in a job, on a PTY
// or with the shell's streams, as the command's place in the line decides.
// A start failure comes back as a *startError.
type commandRun func(path string, argv []string) error

// commandRunner is how the command name, which a handler call in ctx runs, runs
// (commandRun).
func (e *Executor) commandRunner(ctx context.Context, hc interp.HandlerContext, name string) commandRun {
	newCmd := func(path string, argv []string) *exec.Cmd {
		cmd := exec.Command(path, argv[1:]...) // stopped on cancel by runCommand, runWithPTY
		cmd.Args[0] = argv[0]
		cmd.Dir = e.commandDir(hc.Dir)
		cmd.Env = environToSlice(hc.Env)
		cmd.Env = append(cmd.Env, "HASH_SHELL=1", "SHELL="+e.shellPath)
		cmd.Env = append(cmd.Env, dirEnv(hc.Env)...)
		return cmd
	}

	if inBackgroundJob(ctx) {
		// Detached: no PTY, no terminal stdin, and no say in the line's
		// status; the job may outlive the line that started it, and
		// hash (jobWriter).
		return func(path string, argv []string) error {
			cmd := newCmd(path, argv)
			cmd.Stdin = e.detachedStdin(hc.Stdin)
			cmd.Stdout, cmd.Stderr = hc.Stdout, hc.Stderr
			if lr := lineRunFrom(ctx); lr != nil {
				cmd.Stdout, cmd.Stderr = lr.jobWriter(hc.Stdout), lr.jobWriter(hc.Stderr)
			}
			return e.runJob(ctx, cmd)
		}
	}

	lr := lineRunFrom(ctx)
	if lr != nil {
		lr.status.setProgram(filepath.Base(name))
	}
	pc := e.detectPipelineContext(hc, lr)

	if pc.needsPTY() {
		// PTY mode: skip OSC 133;C to avoid interference with TUI apps
		// that send their own terminal queries on startup
		return func(path string, argv []string) error {
			return e.runWithPTY(ctx, newCmd(path, argv), hc, pc)
		}
	}

	// Non-PTY: mark the boundary between the user's input and the
	// command's output (OSC 133;C), on the interactive shell's terminal.
	if pc.interactive {
		os.Stdout.WriteString("\x1b]133;C\x07")
	}
	if pc.writesToTerminal() {
		return func(path string, argv []string) error {
			cmd := newCmd(path, argv)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = hc.Stdin, pc.stdoutTerm, pc.stderrTerm
			if pc.stderrTerm == nil {
				cmd.Stderr = lr.output.commandWriter(hc.Stderr) // redirected or piped
			}
			lr.uncaptured.Store(true)
			// The command draws on the terminal: no progress bar meanwhile.
			lr.status.drawStarted()
			defer lr.status.drawEnded()
			return e.runCommand(ctx, cmd)
		}
	}
	return func(path string, argv []string) error {
		// Non-PTY: connect to handler's stdio
		cmd := newCmd(path, argv)
		cmd.Stdin = hc.Stdin
		cmd.Stdout, cmd.Stderr = hc.Stdout, hc.Stderr
		if lr != nil {
			cmd.Stdout, cmd.Stderr = lr.output.commandWriter(hc.Stdout), lr.output.commandWriter(hc.Stderr)
		}
		return e.runCommand(ctx, cmd)
	}
}

// environToSlice converts expand.Environ to a child's environment.
func environToSlice(env expand.Environ) []string {
	var result []string
	env.Each(func(name string, vr expand.Variable) bool {
		if vr.Exported {
			result = append(result, name+"="+vr.String())
		}
		return true
	})
	return result
}

// SetExportedEnv sets an exported environment variable for the shell session.
func (e *Executor) SetExportedEnv(name, value string) {
	if e == nil {
		return
	}
	if e.env != nil {
		e.env.setExportedString(name, value)
	}
	_ = os.Setenv(name, value)
}

// SetPositionalArgs sets $0, $1, $2, etc. for script execution (hash -c).
// $0 names each line Execute parses; the rest become the runner's
// parameters, now or when the first Execute creates it.
func (e *Executor) SetPositionalArgs(args []string) {
	if e == nil {
		return
	}
	e.runnerMu.Lock()
	defer e.runnerMu.Unlock()
	e.positionalArgs = args
	if e.runner != nil {
		_ = positionalParams(args)(e.runner) // "--" with plain words cannot fail
	}
}

// positionalParams sets $1, $2, ... from args[1:]; args[0] is $0.
func positionalParams(args []string) interp.RunnerOption {
	params := []string{"--"}
	if len(args) > 1 {
		params = append(params, args[1:]...)
	}
	return func(r *interp.Runner) error {
		if err := interp.Params(params...)(r); err != nil {
			return err
		}
		if len(params) == 1 {
			r.Params = nil // interp.Params leaves them alone for a bare "--"
		}
		return nil
	}
}

func (e *Executor) updateEnvFromRunner(runner *interp.Runner) {
	if e == nil || e.env == nil || runner == nil {
		return
	}
	e.env.replace(runner.Vars)
}

func (e *Executor) ensureShellEnv() {
	if e == nil || e.env == nil {
		return
	}
	e.env.setExportedString("HASH_SHELL", "1")
	exportDirVars(e.env)
	if e.shellPath != "" {
		e.env.setExportedString("SHELL", e.shellPath)
	}
}

func (e *Executor) syncProcessEnv() {
	if e == nil || e.env == nil {
		return
	}
	e.env.Each(func(name string, vr expand.Variable) bool {
		if vr.Exported && vr.Set && vr.Kind == expand.String {
			_ = os.Setenv(name, vr.Str)
		} else {
			_ = os.Unsetenv(name)
		}
		return true
	})
	_ = os.Setenv("HASH_SHELL", "1")
	if e.shellPath != "" {
		_ = os.Setenv("SHELL", e.shellPath)
	}
}

// syncWorkingDir moves hash's own process to the shell's directory, the
// runner's, at the end of a line, when a move the shell's cd did not make
// (the interpreter's pushd and popd) left them apart. $PWD is the shell's
// variable only: setting it moves nothing, as in bash.
func (e *Executor) syncWorkingDir() {
	if e.runner != nil {
		e.followDir(e.runner.Dir)
	}
}

// followShellDir moves hash's own process to the shell's directory when a
// call the shell itself makes (not a subshell's or a job's) finds them
// apart: pushd and popd moved the shell alone, and the command after them
// must start where the shell is, even in a directory deleted since (`pushd
// d; rmdir ../d; ls`), as it does after cd (enterDir).
func (e *Executor) followShellDir(ctx context.Context) {
	if hc := interp.HandlerCtx(ctx); handlerRunner(hc) == e.runner {
		e.followDir(hc.Dir)
	}
}

// followDir enters dir, the shell's directory, unless hash is there.
func (e *Executor) followDir(dir string) {
	if own := e.dir.Load(); own == nil || *own != dir {
		_ = e.enterDir(dir)
	}
}

// enterDir moves hash's own process to dir, the shell's new directory, and
// records it (e.dir). The shell's cd calls it as it goes there (cdCall),
// so that the commands the shell runs from then on inherit it
// (commandDir), even once it was deleted, as bash's do.
func (e *Executor) enterDir(dir string) error {
	if err := os.Chdir(dir); err != nil {
		return err
	}
	e.dir.Store(&dir)
	return nil
}

// commandDir is the directory to start a command whose shell is in dir
// with: none ("", the command inherits hash's own) when dir is the one
// hash is in, as bash's commands inherit the shell's, so that one deleted
// since still works (Go's start would fail to enter it).
func (e *Executor) commandDir(dir string) string {
	if own := e.dir.Load(); own != nil && *own == dir {
		return ""
	}
	return dir
}

// unenterableDir is the refusal of the command name, whose working
// directory dir (commandDir) cannot be entered; nil when it can.
func unenterableDir(name, dir string) error {
	if dir == "" {
		return nil
	}
	_, err := os.Stat(dir)
	var errno syscall.Errno
	if err == nil || !errors.As(err, &errno) {
		return nil
	}
	return &CommandNotExecutableError{Command: name, Reason: "cannot enter the working directory " + dir + ": " + jobexec.Strerror(errno)}
}

// shellQuote quotes a string for safe use in shell commands.
func shellQuote(s string) string {
	// Use single quotes and escape any single quotes within
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// normalizeExternalCommandError turns a finished external command's error
// into the exit status the shell sets. A command killed by a signal sets
// 128+signal, as in bash (130 after Ctrl+C).
func normalizeExternalCommandError(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return interp.ExitStatus(exitStatusOf(exitErr.ProcessState))
	}
	return err
}

// exitStatusOf is a finished process's shell exit status.
func exitStatusOf(ps *os.ProcessState) uint8 {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return uint8(128 + int(ws.Signal())&0x7f) //nolint:gosec // G115: masked to 0..255
	}
	code := ps.ExitCode()
	if code < 0 || code > 255 {
		return 1
	}
	return uint8(code) //nolint:gosec // G115: checked to be within 0..255 above
}

// exitCodeFromError extracts exit code from interpreter error.
func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	var status interp.ExitStatus
	if errors.As(err, &status) {
		return int(status)
	}
	return 1
}

// formatBytes formats a byte count in a human-readable format.
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// BuiltinFunc implements a Hash builtin that runs inside the interpreter.
// Output goes to the writers the interpreter supplies, so the builtin
// composes with pipelines and redirections like any other command.
type BuiltinFunc func(ctx context.Context, args []string, stdout, stderr io.Writer) error

// RegisterBuiltin makes fn available as the command name inside the
// interpreter. Register builtins before executing commands.
func (e *Executor) RegisterBuiltin(name string, fn BuiltinFunc) {
	if e.builtins == nil {
		e.builtins = make(map[string]BuiltinFunc)
	}
	e.builtins[name] = fn
}

// SimpleCommandWords reports whether line is a single simple command made of
// literal words only, returning those words. Lines with pipes, redirections,
// control operators, expansions, globs, escapes or assignments are not
// simple: the shell would transform them, so a caller that treats words
// literally must leave them to the interpreter.
func (e *Executor) SimpleCommandWords(line string) ([]string, bool) {
	file, err := shparse.Parse(e.newParser(), line, "")
	if err != nil || len(file.Stmts) != 1 {
		return nil, false
	}
	stmt := file.Stmts[0]
	if stmt.Background || stmt.Negated || stmt.Coprocess || len(stmt.Redirs) > 0 {
		return nil, false
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) > 0 || len(call.Args) == 0 {
		return nil, false
	}
	words := make([]string, 0, len(call.Args))
	for _, arg := range call.Args {
		word, ok := literalWord(arg)
		if !ok {
			return nil, false
		}
		words = append(words, word)
	}
	return words, true
}

// literalWord returns the text of a word made only of literals and quoted
// literals. Unquoted literals containing glob metacharacters or backslash
// escapes are rejected, since the shell would still transform them.
func literalWord(w *syntax.Word) (string, bool) {
	var b strings.Builder
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			if strings.ContainsAny(p.Value, "\\*?[") {
				return "", false
			}
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, inner := range p.Parts {
				lit, ok := inner.(*syntax.Lit)
				if !ok {
					return "", false
				}
				b.WriteString(lit.Value)
			}
		default:
			return "", false
		}
	}
	return b.String(), true
}

// UseInput makes f the shell's own input in place of os.Stdin: the
// terminal a PTY command's keys come from, and what a line reads.
func (e *Executor) UseInput(f *os.File) {
	e.stdin = f
}
