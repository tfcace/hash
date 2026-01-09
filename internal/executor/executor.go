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
	"github.com/tfcace/hash/internal/progress"
	"golang.org/x/term"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// defaultCaptureSize limits how much output we capture by default (1MB).
const defaultCaptureSize = 1024 * 1024

// limitedWriter wraps a writer and stops writing after n bytes.
type limitedWriter struct {
	w io.Writer
	n int64
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}
	origLen := len(p)
	if int64(origLen) > l.n {
		p = p[:l.n]
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
	var readSet syscall.FdSet
	readSet.Bits[fd/64] |= 1 << (uint(fd) % 64)

	tv := syscall.NsecToTimeval(timeout.Nanoseconds())

	if _, err := syscall.Select(fd+1, &readSet, nil, nil, &tv); err != nil {
		return false
	}

	return (readSet.Bits[fd/64] & (1 << (uint(fd) % 64))) != 0
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
}

// New creates a new Executor.
func New() *Executor {
	execPath, _ := os.Executable()
	env := newEnvStoreFromOS()
	exec := &Executor{
		shellName:         "hash",
		shellPath:         execPath,
		progressOSC:       progress.NewOSC(os.Stdout),
		progressThreshold: 2 * time.Second,
		env:               env,
		captureLimit:      defaultCaptureSize,
	}
	exec.ensureShellEnv()
	exec.syncProcessEnv()
	return exec
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
	switch {
	case e.captureLimit == 0:
		captureWriter = io.Discard
	case e.captureLimit > 0:
		captureWriter = &limitedWriter{w: &captureBuf, n: e.captureLimit}
	default:
		captureWriter = &captureBuf
	}

	// Set up output writers with capture
	var actualStdout io.Writer = io.Discard
	if stdout != nil {
		actualStdout = io.MultiWriter(stdout, captureWriter)
	} else {
		actualStdout = captureWriter
	}

	actualStderr := stderr
	if actualStderr == nil {
		actualStderr = io.Discard
	}

	// Create interpreter with options
	opts := []interp.RunnerOption{
		interp.StdIO(os.Stdin, actualStdout, actualStderr),
		interp.Env(e.env),
		interp.ExecHandlers(e.execHandler),
	}

	// Add positional parameters if set
	// For -c behavior: first arg becomes $0, rest become $1, $2, ...
	if len(e.positionalArgs) > 1 {
		// Remaining args ($1, $2, ...) go via interp.Params
		paramsArgs := append([]string{"--"}, e.positionalArgs[1:]...)
		opts = append(opts, interp.Params(paramsArgs...))
	}

	runner, err := interp.New(opts...)
	if err != nil {
		return nil, err
	}

	// Run the command
	err = runner.Run(ctx, prog)
	e.updateEnvFromRunner(runner)
	e.ensureShellEnv()
	e.syncProcessEnv()
	e.syncWorkingDir()

	return &Result{
		ExitCode:       exitCodeFromError(err),
		Duration:       time.Since(start),
		Command:        command,
		CapturedOutput: captureBuf.String(),
	}, nil
}

// execHandler spawns external commands with PTY when needed.
func (e *Executor) execHandler(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		hc := interp.HandlerCtx(ctx)

		path, err := interp.LookPathDir(hc.Dir, hc.Env, args[0])
		if err != nil {
			return err
		}

		cmd := exec.CommandContext(ctx, path, args[1:]...)
		cmd.Dir = hc.Dir
		cmd.Env = environToSlice(hc.Env)
		cmd.Env = append(cmd.Env, "HASH_SHELL=1", "SHELL="+e.shellPath)

		// Use PTY only if stdin/stdout are terminals (interactive)
		// We check the actual terminal fds, not hc.Stdin/hc.Stdout which may be wrapped
		stdinFd := int(os.Stdin.Fd())
		stdoutFd := int(os.Stdout.Fd())

		if term.IsTerminal(stdinFd) && term.IsTerminal(stdoutFd) {
			return e.runWithPTY(ctx, cmd, hc)
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
