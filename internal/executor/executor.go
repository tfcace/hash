package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
	"github.com/tfcace/hash/internal/progress"
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

type deadlineReader interface {
	SetReadDeadline(time.Time) error
}

const (
	stdinCopyPoll   = 50 * time.Millisecond
	ctrlCByte       = 0x03
	doubleCtrlCWait = 750 * time.Millisecond
	ptyStopGrace    = 200 * time.Millisecond
	ptyTraceTick    = 5 * time.Second
)

var errCopyDone = errors.New("copy done")

func supportsReadDeadline(r io.Reader) (deadlineReader, bool) {
	dr, ok := r.(deadlineReader)
	if !ok {
		return nil, false
	}
	if err := dr.SetReadDeadline(time.Now()); err != nil {
		_ = dr.SetReadDeadline(time.Time{})
		return nil, false
	}
	_ = dr.SetReadDeadline(time.Time{})
	return dr, true
}

type ptyTrace struct {
	path         string
	f            *os.File
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

func ptyTracePath() string {
	if path := strings.TrimSpace(os.Getenv("HASH_PTY_TRACE_PATH")); path != "" {
		return path
	}
	wd, err := os.Getwd()
	if err != nil {
		return "hash-pty-trace.log"
	}
	return filepath.Join(wd, "hash-pty-trace.log")
}

func newPTYTrace(cmd *exec.Cmd, ptmx *os.File) *ptyTrace {
	if !ptyTraceEnabled() {
		return nil
	}

	path := ptyTracePath()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}

	t := &ptyTrace{path: path, f: f}
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
	return t
}

func (t *ptyTrace) Close() {
	if t == nil {
		return
	}
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

func (t *ptyTrace) monitor(stop <-chan struct{}, stopped chan<- struct{}) {
	if t == nil {
		return
	}
	ticker := time.NewTicker(ptyTraceTick)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			t.logStatus("trace stop")
			if stopped != nil {
				close(stopped)
			}
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

func copyWithDeadline(dst io.Writer, src io.Reader, dr deadlineReader, done <-chan struct{}, onInput func([]byte), onWrite func(int)) error {
	buf := make([]byte, 4096)
	for {
		select {
		case <-done:
			_ = dr.SetReadDeadline(time.Time{})
			return errCopyDone
		default:
		}

		_ = dr.SetReadDeadline(time.Now().Add(stdinCopyPoll))
		n, err := src.Read(buf)
		_ = dr.SetReadDeadline(time.Time{})
		if n > 0 {
			if onInput != nil {
				onInput(buf[:n])
			}
			if werr := writeAll(dst, buf[:n]); werr != nil {
				if isRetryableIO(werr) {
					continue
				}
				return werr
			}
			if onWrite != nil {
				onWrite(n)
			}
		}
		if err != nil {
			if isRetryableIO(err) {
				continue
			}
			return err
		}
	}
}

func copyWithPoll(dst io.Writer, src *os.File, done <-chan struct{}, onInput func([]byte), onWrite func(int)) error {
	buf := make([]byte, 4096)
	fd := int(src.Fd())
	for {
		select {
		case <-done:
			return errCopyDone
		default:
		}

		if !hasDataAvailable(fd, stdinCopyPoll) {
			continue
		}

		n, err := src.Read(buf)
		if n > 0 {
			if onInput != nil {
				onInput(buf[:n])
			}
			if werr := writeAll(dst, buf[:n]); werr != nil {
				if isRetryableIO(werr) {
					continue
				}
				return werr
			}
			if onWrite != nil {
				onWrite(n)
			}
		}
		if err != nil {
			if isRetryableIO(err) {
				continue
			}
			return err
		}
	}
}

func copyWithOnInput(dst io.Writer, src io.Reader, onInput func([]byte), onWrite func(int)) error {
	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if onInput != nil {
				onInput(buf[:n])
			}
			if werr := writeAll(dst, buf[:n]); werr != nil {
				if isRetryableIO(werr) {
					continue
				}
				return werr
			}
			if onWrite != nil {
				onWrite(n)
			}
		}
		if err != nil {
			if isRetryableIO(err) {
				continue
			}
			return err
		}
	}
}

func copyWithRetry(dst io.Writer, src io.Reader, onRead func(int), onWrite func(int)) error {
	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if onRead != nil {
				onRead(n)
			}
			if werr := writeAll(dst, buf[:n]); werr != nil {
				if isRetryableIO(werr) {
					continue
				}
				return werr
			}
			if onWrite != nil {
				onWrite(n)
			}
		}
		if err != nil {
			if isRetryableIO(err) {
				continue
			}
			if errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) || errors.Is(err, syscall.EIO) {
				return nil
			}
			return err
		}
	}
}

func hasDataAvailable(fd int, timeout time.Duration) bool {
	var readSet unix.FdSet
	readSet.Set(fd)

	tv := unix.NsecToTimeval(timeout.Nanoseconds())

	if _, err := unix.Select(fd+1, &readSet, nil, nil, &tv); err != nil {
		return false
	}

	return readSet.IsSet(fd)
}

// drainTerminalResponses discards any pending terminal responses from stdin.
// This should be called before starting PTY I/O to prevent escape sequences
// (like DECRQSS responses from colorprofile/lipgloss queries) from being read
// by the child process and appearing as garbage input.
//
// The issue: Libraries like charmbracelet/colorprofile query terminal capabilities
// (e.g., current SGR state via DECRQSS). The terminal responds asynchronously with
// escape sequences. If a PTY command starts before these responses are consumed,
// they get read by the child process instead.
func drainTerminalResponses(fd int, trace *ptyTrace) {
	// Wait briefly for any terminal responses that may still be in flight.
	// Terminal responses typically arrive within 10-50ms of the query.
	if !hasDataAvailable(fd, 50*time.Millisecond) {
		// No pending data
		return
	}

	buf := make([]byte, 512)
	drained := 0
	for {
		if !hasDataAvailable(fd, 5*time.Millisecond) {
			break
		}

		n, err := syscall.Read(fd, buf)
		if err != nil || n <= 0 {
			break
		}

		drained += n

		// Only drain data that looks like terminal responses (escape sequences).
		// Terminal responses start with ESC (0x1b), DCS (0x90), or CSI (0x9b).
		// If we see regular printable input, stop - don't discard user keystrokes.
		if buf[0] != 0x1b && buf[0] != 0x90 && buf[0] != 0x9b {
			if trace != nil {
				trace.logf("drain: stopped at non-escape byte 0x%02x after %d bytes", buf[0], drained)
			}
			// This might be user input - but we already read it, so it's lost.
			// This is acceptable since escape sequences are more likely than
			// the user typing a command and immediately pressing enter.
			break
		}

		if trace != nil {
			trace.logf("drain: read %d bytes: %q", n, buf[:n])
		}
	}

	if trace != nil && drained > 0 {
		trace.logf("drain: discarded %d total bytes of terminal responses", drained)
	}
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
}

// Executor runs shell commands using mvdan/sh interpreter.
type Executor struct {
	shellName         string
	shellPath         string
	progressOSC       *progress.OSC
	progressThreshold time.Duration
	env               *envStore
	positionalArgs    []string    // $0, $1, $2, etc. for -c execution
	ptyActive         atomic.Bool // set when PTY is in use, disables progress
	captureLimit      int64

	// Persistent interpreter state - keeps function definitions across executions
	runner       *interp.Runner
	switchStdout *switchableWriter
	switchStderr *switchableWriter
	runnerMu     sync.Mutex // Protects runner access during execution
}

// New creates a new Executor.
func New() *Executor {
	execPath, _ := os.Executable()
	env := newEnvStoreFromOS()

	// Create switchable writers for persistent runner
	switchStdout := newSwitchableWriter(os.Stdout)
	switchStderr := newSwitchableWriter(os.Stderr)

	exec := &Executor{
		shellName:         "hash",
		shellPath:         execPath,
		progressOSC:       progress.NewOSC(os.Stdout),
		progressThreshold: 2 * time.Second,
		env:               env,
		captureLimit:      defaultCaptureSize,
		switchStdout:      switchStdout,
		switchStderr:      switchStderr,
		// runner is created lazily on first Execute()
	}
	exec.ensureShellEnv()
	exec.syncProcessEnv()
	return exec
}

// initRunner creates the persistent interpreter runner.
// Called lazily on first Execute() to allow configuration before first use.
func (e *Executor) initRunner() error {
	opts := []interp.RunnerOption{
		interp.StdIO(os.Stdin, e.switchStdout, e.switchStderr),
		interp.Env(e.env),
		interp.ExecHandlers(e.execHandler),
	}

	// Set positional parameters ($1, $2, etc.) if provided
	// Skip $0 since that's handled separately via syntax.Parse filename
	if len(e.positionalArgs) > 1 {
		// interp.Params expects "--" followed by the parameters
		params := append([]string{"--"}, e.positionalArgs[1:]...)
		opts = append(opts, interp.Params(params...))
	}

	runner, err := interp.New(opts...)
	if err != nil {
		return fmt.Errorf("failed to create interpreter: %w", err)
	}

	e.runner = runner
	return nil
}

// Reset clears the interpreter state, including all function definitions.
// The runner will be recreated on the next Execute() call.
func (e *Executor) Reset() {
	e.runnerMu.Lock()
	defer e.runnerMu.Unlock()
	e.runner = nil
}

// SetProgressThreshold sets how long to wait before showing progress.
func (e *Executor) SetProgressThreshold(d time.Duration) {
	e.progressThreshold = d
}

// SetProgressEnabled enables or disables progress bar.
func (e *Executor) SetProgressEnabled(enabled bool) {
	e.progressOSC.SetEnabled(enabled)
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

// Execute runs a command using the mvdan/sh interpreter.
func (e *Executor) Execute(ctx context.Context, command string, stdout, stderr io.Writer) (*Result, error) {
	e.runnerMu.Lock()
	defer e.runnerMu.Unlock()

	start := time.Now()
	e.ensureShellEnv()
	e.ptyActive.Store(false)

	// Progress timer - only for non-PTY commands (wget, curl, etc.)
	// PTY commands (vim, helix) handle their own display
	var progressShown atomic.Bool
	progressDone := make(chan struct{})
	go func() {
		select {
		case <-time.After(e.progressThreshold):
			// Don't show progress if PTY is active
			if !e.ptyActive.Load() {
				e.progressOSC.Start()
				progressShown.Store(true)
			}
		case <-progressDone:
		}
	}()
	defer func() {
		close(progressDone)
		if progressShown.Load() {
			e.progressOSC.Done()
		}
	}()

	// Determine $0 value - first positional arg or shell name
	shellName := e.shellName
	if len(e.positionalArgs) > 0 {
		shellName = e.positionalArgs[0]
	}

	// Parse the command with $0 name
	prog, err := syntax.NewParser().Parse(strings.NewReader(command), shellName)
	if err != nil {
		return nil, err
	}

	// Set up capture buffer
	var captureBuf bytes.Buffer
	var captureWriter io.Writer = &captureBuf
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

	// Switch writers for this execution
	if stdout != nil {
		e.switchStdout.Set(io.MultiWriter(stdout, captureWriter))
	} else {
		e.switchStdout.Set(captureWriter)
	}
	if stderr != nil {
		e.switchStderr.Set(stderr)
	} else {
		e.switchStderr.Set(io.Discard)
	}

	// Create persistent runner on first use (lazy init)
	if e.runner == nil {
		if err := e.initRunner(); err != nil {
			return nil, err
		}
	}

	// Run with persistent runner - functions persist across executions
	err = e.runner.Run(ctx, prog)
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

	// Capture PTY usage before returning
	usedPTY := e.ptyActive.Load()

	// Return CommandNotFoundError to caller for special handling
	var cnf *CommandNotFoundError
	if errors.As(err, &cnf) {
		return &Result{
			ExitCode:       127,
			Duration:       time.Since(start),
			Command:        command,
			CapturedOutput: captureBuf.String(),
			UsedPTY:        usedPTY,
		}, cnf
	}

	return &Result{
		ExitCode:       exitCodeFromError(err),
		Duration:       time.Since(start),
		Command:        command,
		CapturedOutput: captureBuf.String(),
		UsedPTY:        usedPTY,
	}, nil
}

// execHandler spawns external commands with PTY when needed.
func (e *Executor) execHandler(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		hc := interp.HandlerCtx(ctx)

		path, err := interp.LookPathDir(hc.Dir, hc.Env, args[0])
		if err != nil {
			return &CommandNotFoundError{Command: args[0]}
		}

		cmd := exec.CommandContext(ctx, path, args[1:]...)
		cmd.Dir = hc.Dir
		cmd.Env = environToSlice(hc.Env)
		cmd.Env = append(cmd.Env, "HASH_SHELL=1", "SHELL="+e.shellPath)

		// Use PTY if running interactively in a terminal and not in a pipeline.
		// Check real terminal (os.Stdin/os.Stdout) and hc.Stdin to detect pipeline.
		// Note: hc.Stdout is always switchableWriter (not *os.File), so we can't
		// check it directly. Instead, we check hc.Stdin to detect if we're
		// downstream in a pipeline (e.g., `cat | this_cmd`).
		// Use PTY if stdin is a terminal and not in a pipeline.
		// Note: We only check stdin because stdout may be captured/redirected
		// by the parent process (e.g., Claude Code) while still being interactive.
		stdinFd := int(os.Stdin.Fd())
		stdinIsTerm := term.IsTerminal(stdinFd)

		// Check if hc.Stdin is a terminal (detects downstream pipe position)
		hcStdinTerminal := false
		if f, ok := hc.Stdin.(*os.File); ok {
			hcStdinTerminal = term.IsTerminal(int(f.Fd()))
		}

		if stdinIsTerm && hcStdinTerminal {
			// PTY mode: skip OSC 133;C to avoid interference with TUI apps
			// that send their own terminal queries on startup
			return e.runWithPTY(ctx, cmd, hc)
		}

		// Non-PTY: emit OSC 133;C only if running interactively.
		// This marks the boundary between user input and command output.
		// Skip when running in non-interactive contexts where the sequence
		// could interfere with programs that check terminal state.
		if stdinIsTerm {
			os.Stdout.WriteString("\x1b]133;C\x07")
		}

		// Non-PTY: connect to handler's stdio
		cmd.Stdin = hc.Stdin
		cmd.Stdout = hc.Stdout
		cmd.Stderr = hc.Stderr

		return cmd.Run()
	}
}

// runWithPTY runs a command with a pseudo-terminal.
func (e *Executor) runWithPTY(ctx context.Context, cmd *exec.Cmd, hc interp.HandlerContext) error {
	ptmx, err := pty.Start(cmd)
	if err != nil {
		cmd.Stdin = hc.Stdin
		cmd.Stdout = hc.Stdout
		cmd.Stderr = hc.Stderr
		return cmd.Run()
	}
	defer ptmx.Close()

	trace := newPTYTrace(cmd, ptmx)
	if trace != nil {
		defer trace.Close()
	}

	// Signal PTY is active to disable progress indicator
	e.ptyActive.Store(true)

	// Put the real terminal in raw mode so keystrokes are passed through
	// character-by-character to the PTY, rather than being line-buffered.
	// This is required for interactive programs like vim, helix, claude, etc.
	stdinFd := int(os.Stdin.Fd())
	if trace != nil {
		trace.logf("terminal stdin fd=%d is_tty=%t", stdinFd, term.IsTerminal(stdinFd))
	}
	if term.IsTerminal(stdinFd) {
		oldState, err := term.MakeRaw(stdinFd)
		if err == nil {
			defer term.Restore(stdinFd, oldState)
		}
	}

	// Handle terminal resize - propagate SIGWINCH to PTY
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	// Set initial PTY size
	if w, h, err := term.GetSize(stdinFd); err == nil {
		pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(h), Cols: uint16(w)})
		if trace != nil {
			trace.logf("pty size set rows=%d cols=%d", h, w)
		}
	}

	// Handle resize signals
	go func() {
		for range sigCh {
			if w, h, err := term.GetSize(stdinFd); err == nil {
				pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(h), Cols: uint16(w)})
			}
		}
	}()

	// Drain any pending terminal responses before starting stdin copy.
	// Libraries like bubbletea/colorprofile query terminal capabilities (DECRQSS, etc.)
	// and responses may still be in the input buffer. Without draining, these responses
	// would be read by the PTY child process and appear as garbage input.
	drainTerminalResponses(stdinFd, trace)

	// Stop stdin->PTY copy on command exit so it doesn't consume the next prompt.
	done := make(chan struct{})
	stdinDone := make(chan struct{})
	cancelable := false
	var traceStop chan struct{}
	var traceStopped chan struct{}
	if trace != nil {
		traceStop = make(chan struct{})
		traceStopped = make(chan struct{})
		go trace.monitor(traceStop, traceStopped)
	}
	var lastCtrlC time.Time
	onInput := func(buf []byte) {
		if trace != nil {
			trace.markInRead(len(buf))
		}
		for _, b := range buf {
			if b != ctrlCByte {
				continue
			}
			now := time.Now()
			if !lastCtrlC.IsZero() && now.Sub(lastCtrlC) <= doubleCtrlCWait {
				signalProcessGroup(cmd, syscall.SIGINT)
			}
			lastCtrlC = now
		}
	}
	onStdinWrite := func(n int) {
		if trace != nil {
			trace.markInWrite(n)
		}
	}
	if dr, ok := supportsReadDeadline(hc.Stdin); ok {
		cancelable = true
		go func() {
			err := copyWithDeadline(ptmx, hc.Stdin, dr, done, onInput, onStdinWrite)
			if trace != nil {
				trace.logf("stdin->pty copy (deadline) stopped: %s", describeCopyEnd(err))
			}
			close(stdinDone)
		}()
	} else if f, ok := hc.Stdin.(*os.File); ok {
		cancelable = true
		go func() {
			err := copyWithPoll(ptmx, f, done, onInput, onStdinWrite)
			if trace != nil {
				trace.logf("stdin->pty copy (poll) stopped: %s", describeCopyEnd(err))
			}
			close(stdinDone)
		}()
	} else {
		go func() {
			err := copyWithOnInput(ptmx, hc.Stdin, onInput, onStdinWrite)
			if trace != nil {
				trace.logf("stdin->pty copy stopped: %s", describeCopyEnd(err))
			}
			close(stdinDone)
		}()
	}

	stdoutDone := make(chan error, 1)
	go func() {
		err := copyWithRetry(hc.Stdout, ptmx,
			func(n int) {
				if trace != nil {
					trace.markOutRead(n)
				}
			},
			func(n int) {
				if trace != nil {
					trace.markOutWrite(n)
				}
			},
		)
		if trace != nil {
			trace.logf("pty->stdout copy stopped: %s", describeCopyEnd(err))
		}
		stdoutDone <- err
	}()

	cmdDone := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		if trace != nil {
			trace.logf("cmd.Wait returned err=%v", err)
		}
		cmdDone <- err
	}()

	var cmdErr error
	var stdoutErr error
	stdoutFinished := false

	select {
	case cmdErr = <-cmdDone:
	case stdoutErr = <-stdoutDone:
		stdoutFinished = true
		select {
		case cmdErr = <-cmdDone:
		case <-time.After(ptyStopGrace):
			if trace != nil {
				trace.logf("pty->stdout ended early; sending SIGTERM to process group")
			}
			signalProcessGroup(cmd, syscall.SIGTERM)
			cmdErr = <-cmdDone
			if cmdErr == nil && stdoutErr != nil {
				cmdErr = stdoutErr
			}
		}
	}

	if trace != nil {
		trace.logf("closing stdin copy")
	}
	close(done)
	if cancelable {
		<-stdinDone
	}
	if traceStop != nil {
		close(traceStop)
		<-traceStopped
	}

	if !stdoutFinished {
		select {
		case stdoutErr = <-stdoutDone:
			stdoutFinished = true
		case <-time.After(ptyStopGrace):
			if trace != nil {
				trace.logf("pty->stdout still running; closing ptmx")
			}
			_ = ptmx.Close()
			stdoutErr = <-stdoutDone
			stdoutFinished = true
		}
	}

	if cmdErr == nil && stdoutErr != nil {
		return stdoutErr
	}
	return cmdErr
}

// environToSlice converts expand.Environ to []string.
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

// SetPositionalArgs sets $0, $1, $2, etc. for script execution.
// These are used when creating the interpreter for -c command execution.
func (e *Executor) SetPositionalArgs(args []string) {
	if e == nil {
		return
	}
	e.positionalArgs = args
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

// syncWorkingDir syncs the process working directory with PWD from the environment.
// This ensures cd commands executed by the interpreter affect the actual process.
func (e *Executor) syncWorkingDir() {
	if e == nil || e.env == nil {
		return
	}
	pwd := e.env.Get("PWD")
	if pwd.Set && pwd.Kind == expand.String && pwd.Str != "" {
		currentDir, _ := os.Getwd()
		if pwd.Str != currentDir {
			_ = os.Chdir(pwd.Str)
		}
	}
}

// SyncRunnerDir syncs the persistent runner's working directory with the process.
// Call this after changing directory via os.Chdir() (e.g., after builtin cd).
func (e *Executor) SyncRunnerDir() {
	e.runnerMu.Lock()
	defer e.runnerMu.Unlock()

	if e.runner == nil {
		return // Runner will pick up current dir when created
	}

	// Run a cd command through the interpreter to sync its internal directory state
	cwd, err := os.Getwd()
	if err != nil {
		return
	}

	// Parse and run cd to update runner's internal Dir
	prog, err := syntax.NewParser().Parse(strings.NewReader("cd "+shellQuote(cwd)), "")
	if err != nil {
		return
	}

	// Temporarily discard output for this sync command
	e.switchStdout.Set(io.Discard)
	e.switchStderr.Set(io.Discard)
	_ = e.runner.Run(context.Background(), prog)
}

// shellQuote quotes a string for safe use in shell commands.
func shellQuote(s string) string {
	// Use single quotes and escape any single quotes within
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// exitCodeFromError extracts exit code from interpreter error.
func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	if status, ok := interp.IsExitStatus(err); ok {
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
