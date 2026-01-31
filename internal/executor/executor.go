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
	"github.com/tfcace/hash/internal/trace"
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
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644) //nolint:gosec // G302: debug trace logs
	if err != nil {
		return nil //nolint:nilerr // graceful degradation: skip tracing if file can't be opened
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

func copyWithRetry(dst io.Writer, src io.Reader, onRead, onWrite func(int)) error {
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
func drainTerminalResponses(fd int, ptyTr *ptyTrace) {
	// Wait briefly for any terminal responses that may still be in flight.
	// Terminal responses typically arrive within 10-50ms of the query.
	if !hasDataAvailable(fd, 50*time.Millisecond) {
		// No pending data
		return
	}

	buf := make([]byte, 512)
	drained := 0
	for hasDataAvailable(fd, 5*time.Millisecond) {
		n, err := syscall.Read(fd, buf)
		if err != nil || n <= 0 {
			break
		}

		drained += n

		// Only drain data that looks like terminal responses (escape sequences).
		// Terminal responses start with ESC (0x1b), DCS (0x90), or CSI (0x9b).
		// If we see regular printable input, stop - don't discard user keystrokes.
		if buf[0] != 0x1b && buf[0] != 0x90 && buf[0] != 0x9b {
			if ptyTr != nil {
				ptyTr.logf("drain: stopped at non-escape byte 0x%02x after %d bytes", buf[0], drained)
			}
			// This might be user input - but we already read it, so it's lost.
			// This is acceptable since escape sequences are more likely than
			// the user typing a command and immediately pressing enter.
			break
		}

		if ptyTr != nil {
			ptyTr.logf("drain: read %d bytes: %q", n, buf[:n])
		}
	}

	if ptyTr != nil && drained > 0 {
		ptyTr.logf("drain: discarded %d total bytes of terminal responses", drained)
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

	// Function tracking for completion
	functions   map[string]struct{}
	functionsMu sync.RWMutex
}

// New creates a new Executor.
func New() *Executor {
	execPath, _ := os.Executable()
	env := newEnvStoreFromOS()

	// Create switchable writers for persistent runner
	switchStdout := newSwitchableWriter(os.Stdout)
	switchStderr := newSwitchableWriter(os.Stderr)

	e := &Executor{
		shellName:         "hash",
		shellPath:         execPath,
		progressOSC:       progress.NewOSC(os.Stdout),
		progressThreshold: 2 * time.Second,
		env:               env,
		captureLimit:      defaultCaptureSize,
		switchStdout:      switchStdout,
		switchStderr:      switchStderr,
		functions:         make(map[string]struct{}),
		// runner is created lazily on first Execute()
	}
	e.ensureShellEnv()
	e.syncProcessEnv()
	return e
}

// initRunner creates the persistent interpreter runner.
// Called lazily on first Execute() to allow configuration before first use.
func (e *Executor) initRunner() error {
	opts := []interp.RunnerOption{
		interp.StdIO(os.Stdin, e.switchStdout, e.switchStderr),
		interp.Env(e.env),
		interp.CallHandler(e.bashBuiltinHandler),
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

// bashBuiltinHandler intercepts source/. and eval commands to parse with LangBash.
// This uses CallHandler which runs for ALL commands including builtins, unlike
// ExecHandler which only runs for external commands.
// We handle the command ourselves, then return ":" (no-op) so the Runner
// doesn't also try to run the builtin.
func (e *Executor) bashBuiltinHandler(ctx context.Context, args []string) ([]string, error) {
	if len(args) == 0 {
		return args, nil
	}

	cmd := args[0]

	switch cmd {
	case "source", ".":
		// Handle source with LangBash parsing
		if len(args) < 2 {
			hc := interp.HandlerCtx(ctx)
			fmt.Fprintln(hc.Stderr, "source: need filename")
			return []string{"false"}, nil // Return false to indicate error
		}
		trace.Emit("compat", "source_intercept", trace.LevelVerbose, map[string]any{
			"path": args[1],
		})
		err := e.handleBashSource(ctx, args[1])
		if err != nil {
			return []string{"false"}, nil //nolint:nilerr // return "false" to shell, error handled internally
		}
		return []string{":"}, nil // Return no-op, we handled it

	case "eval":
		// Handle eval with LangBash parsing
		if len(args) < 2 {
			return []string{":"}, nil // eval with no args is a no-op
		}
		src := strings.Join(args[1:], " ")
		trace.Emit("compat", "eval_intercept", trace.LevelVerbose, map[string]any{
			"src_preview": truncateForTrace(src, 200),
		})
		err := e.handleBashEval(ctx, args[1:])
		if err != nil {
			return []string{"false"}, nil //nolint:nilerr // return "false" to shell, error handled internally
		}
		return []string{":"}, nil // Return no-op, we handled it

	case "alias":
		// Handle alias definitions that contain bash-specific syntax
		// mvdan/sh's alias builtin parses values with POSIX mode, which fails
		// on bash syntax like && or ||. We convert these to functions instead.
		return e.handleBashAlias(ctx, args[1:])

	case "unset":
		// Track function unsets for completion
		for i, arg := range args {
			if arg == "-f" && i+1 < len(args) {
				// Remove the function from tracking
				for j := i + 1; j < len(args); j++ {
					if !strings.HasPrefix(args[j], "-") {
						e.functionsMu.Lock()
						delete(e.functions, args[j])
						e.functionsMu.Unlock()
					}
				}
			}
		}
		// Let the default handler process the actual unset
		return args, nil

	default:
		// Not source/eval/alias/unset, let Runner handle it normally
		return args, nil
	}
}

// handleBashSource reads a file and executes it with LangBash parsing.
func (e *Executor) handleBashSource(ctx context.Context, path string) error {
	hc := interp.HandlerCtx(ctx)

	// Expand tilde
	if strings.HasPrefix(path, "~") {
		home, _ := os.UserHomeDir()
		if home != "" {
			path = filepath.Join(home, path[1:])
		}
	}

	// Resolve relative paths against current directory
	if !filepath.IsAbs(path) {
		path = filepath.Join(hc.Dir, path)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(hc.Stderr, "source: %v\n", err)
		return err
	}

	// Parse with LangBash - silently skip if it fails (likely zsh-specific syntax)
	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	prog, err := parser.Parse(strings.NewReader(string(content)), path)
	if err != nil {
		// Graceful degradation: skip unparseable files silently
		trace.Emit("compat", "source_parse_skip", trace.LevelVerbose, map[string]any{
			"path":  path,
			"error": err.Error(),
		})
		return nil //nolint:nilerr // intentional: skip unparseable files for shell compatibility
	}

	// Track any function definitions for completion
	e.trackFunctionsFromAST(prog)

	trace.Emit("compat", "source_execute", trace.LevelVerbose, map[string]any{
		"path": path,
	})
	// Run through the existing runner (preserves state)
	return e.runner.Run(ctx, prog)
}

// handleBashEval parses and executes args as bash code.
func (e *Executor) handleBashEval(ctx context.Context, args []string) error {
	src := strings.Join(args, " ")

	// Parse with LangBash - silently skip if it fails (likely zsh-specific syntax)
	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	prog, err := parser.Parse(strings.NewReader(src), "eval")
	if err != nil {
		// Graceful degradation: skip unparseable eval content silently
		trace.Emit("compat", "eval_parse_skip", trace.LevelVerbose, map[string]any{
			"src_preview": truncateForTrace(src, 200),
			"error":       err.Error(),
		})
		return nil //nolint:nilerr // intentional: skip unparseable eval for shell compatibility
	}

	trace.Emit("compat", "eval_execute", trace.LevelVerbose, map[string]any{
		"src_preview": truncateForTrace(src, 100),
	})
	// Run through the existing runner (preserves state)
	return e.runner.Run(ctx, prog)
}

// handleBashAlias handles alias definitions by converting them to functions.
// mvdan/sh stores aliases but doesn't expand them on subsequent Parse() calls
// (each parse is independent). Converting to functions makes them actually work.
// Also handles bash-specific syntax like && that would fail POSIX parsing.
// Returns the args to pass to the default handler, or a no-op if we handled it.
func (e *Executor) handleBashAlias(ctx context.Context, args []string) ([]string, error) {
	trace.Emit("compat", "alias_intercept", trace.LevelVerbose, map[string]any{
		"args": args,
	})

	if len(args) == 0 {
		// No args = show all aliases, let default handler do it
		// (Note: this won't show our function-based aliases)
		return append([]string{"alias"}, args...), nil
	}

	// Check if any arg is a definition (contains =)
	hasDefinition := false
	for _, arg := range args {
		if strings.Contains(arg, "=") {
			hasDefinition = true
			break
		}
	}

	if !hasDefinition {
		// Just displaying aliases, let default handler do it
		return append([]string{"alias"}, args...), nil
	}

	// Process each definition - convert ALL to functions so they actually work
	for _, arg := range args {
		name, value, isDefinition := strings.Cut(arg, "=")
		if !isDefinition {
			// Not a definition, skip
			continue
		}

		// Strip matching outer quotes from value if present
		// Only strip if both start and end match (single or double quotes)
		if len(value) >= 2 {
			if (value[0] == '\'' && value[len(value)-1] == '\'') ||
				(value[0] == '"' && value[len(value)-1] == '"') {
				value = value[1 : len(value)-1]
			}
		}

		// Convert alias to function: alias foo='cmd' becomes foo() { cmd; }
		trace.Emit("compat", "alias_to_function", trace.LevelVerbose, map[string]any{
			"name":  name,
			"value": truncateForTrace(value, 100),
		})

		funcDef := fmt.Sprintf("%s() { %s; }", name, value)
		funcParser := syntax.NewParser(syntax.Variant(syntax.LangBash))
		prog, err := funcParser.Parse(strings.NewReader(funcDef), "alias-func")
		if err != nil {
			// Function conversion failed, skip silently (graceful degradation)
			trace.Emit("compat", "alias_func_fail", trace.LevelVerbose, map[string]any{
				"name":  name,
				"value": truncateForTrace(value, 100),
				"error": err.Error(),
			})
			continue
		}

		// Execute function definition
		if err := e.runner.Run(ctx, prog); err != nil {
			trace.Emit("compat", "alias_func_exec_fail", trace.LevelVerbose, map[string]any{
				"name":  name,
				"error": err.Error(),
			})
		} else {
			// Track the function for completion
			e.functionsMu.Lock()
			e.functions[name] = struct{}{}
			e.functionsMu.Unlock()
		}
	}

	// We handled all definitions ourselves
	return []string{":"}, nil
}

// truncateForTrace truncates a string for trace output.
func truncateForTrace(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
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

// GetRunner returns the underlying interpreter runner (for testing).
func (e *Executor) GetRunner() *interp.Runner {
	return e.runner
}

// Functions returns the names of all tracked user-defined functions.
// This is used for tab-completion of aliases and functions.
func (e *Executor) Functions() []string {
	e.functionsMu.RLock()
	defer e.functionsMu.RUnlock()

	names := make([]string, 0, len(e.functions))
	for name := range e.functions {
		names = append(names, name)
	}
	return names
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

// trackFunctionsFromAST scans a parsed program for function definitions
// and adds them to the tracking map.
func (e *Executor) trackFunctionsFromAST(prog *syntax.File) {
	syntax.Walk(prog, func(node syntax.Node) bool {
		if fn, ok := node.(*syntax.FuncDecl); ok {
			e.functionsMu.Lock()
			e.functions[fn.Name.Value] = struct{}{}
			e.functionsMu.Unlock()
		}
		return true
	})
}

// Execute runs a command using the mvdan/sh interpreter.
//
//nolint:gocyclo // command execution coordinates parsing, capture, and runner lifecycle
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

	// Parse the command with $0 name and bash syntax support
	prog, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), shellName)
	if err != nil {
		return nil, err
	}

	// Track any function definitions for completion
	e.trackFunctionsFromAST(prog)

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
		if initErr := e.initRunner(); initErr != nil {
			return nil, initErr
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

// pipelineContext holds the results of pipeline/terminal detection for command execution.
type pipelineContext struct {
	stdinIsTerm    bool // Real stdin (os.Stdin) is a terminal
	hcStdinIsTerm  bool // Handler's stdin is a terminal (not piped)
	hcStdoutIsPipe bool // Handler's stdout is a pipe (upstream in pipeline)
}

// detectPipelineContext analyzes the handler context to determine terminal/pipeline state.
func detectPipelineContext(hc interp.HandlerContext) pipelineContext {
	pc := pipelineContext{
		stdinIsTerm: term.IsTerminal(int(os.Stdin.Fd())),
	}

	// Check if hc.Stdin is a terminal (detects downstream pipe position)
	if f, ok := hc.Stdin.(*os.File); ok {
		pc.hcStdinIsTerm = term.IsTerminal(int(f.Fd()))
	}

	// Check if hc.Stdout is a pipe (detects upstream pipe position)
	// When in a pipeline like `cmd1 | cmd2`, mvdan/sh sets hc.Stdout
	// to a pipe *os.File for cmd1. We need to know this to disable
	// ONLCR on the PTY to prevent LF→CRLF translation.
	if f, ok := hc.Stdout.(*os.File); ok {
		if fi, err := f.Stat(); err == nil {
			pc.hcStdoutIsPipe = fi.Mode()&os.ModeNamedPipe != 0
		}
	}

	return pc
}

// needsPTY returns true if the command should run with a PTY.
func (pc pipelineContext) needsPTY() bool {
	return pc.stdinIsTerm && pc.hcStdinIsTerm
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

		pc := detectPipelineContext(hc)

		if pc.needsPTY() {
			// PTY mode: skip OSC 133;C to avoid interference with TUI apps
			// that send their own terminal queries on startup
			return e.runWithPTY(ctx, cmd, hc, pc.hcStdoutIsPipe)
		}

		// Non-PTY: emit OSC 133;C only if running interactively.
		// This marks the boundary between user input and command output.
		if pc.stdinIsTerm {
			os.Stdout.WriteString("\x1b]133;C\x07")
		}

		// Non-PTY: connect to handler's stdio
		cmd.Stdin = hc.Stdin
		cmd.Stdout = hc.Stdout
		cmd.Stderr = hc.Stderr

		return cmd.Run()
	}
}

// stdinCopyConfig holds configuration for stdin-to-PTY copying.
type stdinCopyConfig struct {
	ptmx      *os.File
	stdin     io.Reader
	done      <-chan struct{}
	stdinDone chan<- struct{}
	onInput   func([]byte)
	onWrite   func(int)
	ptyTr     *ptyTrace
}

// startStdinCopy starts the appropriate stdin copy goroutine based on stdin type.
// Returns true if the copy is cancelable (supports deadline or poll).
func startStdinCopy(cfg stdinCopyConfig) bool {
	if dr, ok := supportsReadDeadline(cfg.stdin); ok {
		go func() {
			err := copyWithDeadline(cfg.ptmx, cfg.stdin, dr, cfg.done, cfg.onInput, cfg.onWrite)
			if cfg.ptyTr != nil {
				cfg.ptyTr.logf("stdin->pty copy (deadline) stopped: %s", describeCopyEnd(err))
			}
			close(cfg.stdinDone)
		}()
		return true
	}
	if f, ok := cfg.stdin.(*os.File); ok {
		go func() {
			err := copyWithPoll(cfg.ptmx, f, cfg.done, cfg.onInput, cfg.onWrite)
			if cfg.ptyTr != nil {
				cfg.ptyTr.logf("stdin->pty copy (poll) stopped: %s", describeCopyEnd(err))
			}
			close(cfg.stdinDone)
		}()
		return true
	}
	go func() {
		err := copyWithOnInput(cfg.ptmx, cfg.stdin, cfg.onInput, cfg.onWrite)
		if cfg.ptyTr != nil {
			cfg.ptyTr.logf("stdin->pty copy stopped: %s", describeCopyEnd(err))
		}
		close(cfg.stdinDone)
	}()
	return false
}

// setupTerminalRawMode puts the terminal in raw mode and returns a cleanup function.
// Returns nil cleanup if the terminal is not available or setup fails.
func setupTerminalRawMode(stdinFd int, ptyTr *ptyTrace) func() {
	if ptyTr != nil {
		ptyTr.logf("terminal stdin fd=%d is_tty=%t", stdinFd, term.IsTerminal(stdinFd))
	}
	if !term.IsTerminal(stdinFd) {
		return nil
	}
	oldState, err := term.MakeRaw(stdinFd)
	if err != nil {
		return nil
	}
	return func() { term.Restore(stdinFd, oldState) }
}

// setupPTYResize configures PTY size handling and returns a cleanup function.
// It sets the initial PTY size and starts a goroutine to handle SIGWINCH.
func setupPTYResize(stdinFd int, ptmx *os.File, ptyTr *ptyTrace) func() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)

	// Set initial PTY size
	if w, h, err := term.GetSize(stdinFd); err == nil {
		pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(h), Cols: uint16(w)}) //nolint:gosec // G115: terminal sizes are always positive and small
		if ptyTr != nil {
			ptyTr.logf("pty size set rows=%d cols=%d", h, w)
		}
	}

	// Handle resize signals
	go func() {
		for range sigCh {
			if w, h, err := term.GetSize(stdinFd); err == nil {
				pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(h), Cols: uint16(w)}) //nolint:gosec // G115: terminal sizes are always positive and small
			}
		}
	}()

	return func() { signal.Stop(sigCh) }
}

// runWithPTY runs a command with a pseudo-terminal.
// If stdoutIsPipe is true, ONLCR is disabled on the PTY to prevent LF→CRLF
// translation which would corrupt piped data (e.g., `curl ... | bash`).
//
//nolint:gocyclo // PTY coordination requires managing multiple concurrent I/O streams
func (e *Executor) runWithPTY(ctx context.Context, cmd *exec.Cmd, hc interp.HandlerContext, stdoutIsPipe bool) error {
	// When stdout is a pipe, we need to disable ONLCR on the PTY slave.
	// Use pty.Open() to get access to both master and slave for configuration.
	if stdoutIsPipe {
		return e.runWithPTYRaw(ctx, cmd, hc)
	}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		cmd.Stdin = hc.Stdin
		cmd.Stdout = hc.Stdout
		cmd.Stderr = hc.Stderr
		return cmd.Run()
	}
	defer ptmx.Close()

	ptyTr := newPTYTrace(cmd, ptmx)
	if ptyTr != nil {
		defer ptyTr.Close()
	}

	// Signal PTY is active to disable progress indicator
	e.ptyActive.Store(true)

	// Put the real terminal in raw mode
	stdinFd := int(os.Stdin.Fd())
	if cleanup := setupTerminalRawMode(stdinFd, ptyTr); cleanup != nil {
		defer cleanup()
	}

	// Handle terminal resize - propagate SIGWINCH to PTY
	defer setupPTYResize(stdinFd, ptmx, ptyTr)()

	// Drain any pending terminal responses before starting stdin copy.
	// Libraries like bubbletea/colorprofile query terminal capabilities (DECRQSS, etc.)
	// and responses may still be in the input buffer. Without draining, these responses
	// would be read by the PTY child process and appear as garbage input.
	drainTerminalResponses(stdinFd, ptyTr)

	// Stop stdin->PTY copy on command exit so it doesn't consume the next prompt.
	done := make(chan struct{})
	stdinDone := make(chan struct{})
	cancelable := false
	var traceStop chan struct{}
	var traceStopped chan struct{}
	if ptyTr != nil {
		traceStop = make(chan struct{})
		traceStopped = make(chan struct{})
		go ptyTr.monitor(traceStop, traceStopped)
	}
	var lastCtrlC time.Time
	onInput := func(buf []byte) {
		if ptyTr != nil {
			ptyTr.markInRead(len(buf))
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
		if ptyTr != nil {
			ptyTr.markInWrite(n)
		}
	}
	cancelable = startStdinCopy(stdinCopyConfig{
		ptmx:      ptmx,
		stdin:     hc.Stdin,
		done:      done,
		stdinDone: stdinDone,
		onInput:   onInput,
		onWrite:   onStdinWrite,
		ptyTr:     ptyTr,
	})

	stdoutDone := make(chan error, 1)
	go func() {
		err := copyWithRetry(hc.Stdout, ptmx,
			func(n int) {
				if ptyTr != nil {
					ptyTr.markOutRead(n)
				}
			},
			func(n int) {
				if ptyTr != nil {
					ptyTr.markOutWrite(n)
				}
			},
		)
		if ptyTr != nil {
			ptyTr.logf("pty->stdout copy stopped: %s", describeCopyEnd(err))
		}
		stdoutDone <- err
	}()

	cmdDone := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		if ptyTr != nil {
			ptyTr.logf("cmd.Wait returned err=%v", err)
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
			if ptyTr != nil {
				ptyTr.logf("pty->stdout ended early; sending SIGTERM to process group")
			}
			signalProcessGroup(cmd, syscall.SIGTERM)
			cmdErr = <-cmdDone
			if cmdErr == nil && stdoutErr != nil {
				cmdErr = stdoutErr
			}
		}
	}

	if ptyTr != nil {
		ptyTr.logf("closing stdin copy")
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
			// stdout goroutine finished
		case <-time.After(ptyStopGrace):
			if ptyTr != nil {
				ptyTr.logf("pty->stdout still running; closing ptmx")
			}
			_ = ptmx.Close()
			stdoutErr = <-stdoutDone
		}
	}

	if cmdErr == nil && stdoutErr != nil {
		return stdoutErr
	}
	return cmdErr
}

// runWithPTYRaw runs a command with a PTY that has ONLCR disabled.
// This is used when stdout is a pipe to prevent LF→CRLF translation.
//
//nolint:gocyclo // PTY raw mode coordination requires managing multiple concurrent I/O streams
func (e *Executor) runWithPTYRaw(ctx context.Context, cmd *exec.Cmd, hc interp.HandlerContext) error {
	// Use pty.Open() to get access to both master and slave
	ptmx, pts, err := pty.Open()
	if err != nil {
		// Fall back to non-PTY execution
		cmd.Stdin = hc.Stdin
		cmd.Stdout = hc.Stdout
		cmd.Stderr = hc.Stderr
		return cmd.Run()
	}
	defer ptmx.Close()

	// Disable ONLCR on the PTY slave to prevent LF→CRLF translation
	if err := disablePTYOutputProcessing(pts); err != nil {
		pts.Close()
		cmd.Stdin = hc.Stdin
		cmd.Stdout = hc.Stdout
		cmd.Stderr = hc.Stderr
		return cmd.Run()
	}

	// Connect command to PTY slave and start it
	cmd.Stdin = pts
	cmd.Stdout = pts
	cmd.Stderr = pts
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
	cmd.SysProcAttr.Setctty = true

	if err := cmd.Start(); err != nil {
		pts.Close()
		cmd.Stdin = hc.Stdin
		cmd.Stdout = hc.Stdout
		cmd.Stderr = hc.Stderr
		return cmd.Run()
	}
	pts.Close() // Close slave in parent after child has it

	// Signal PTY is active to disable progress indicator
	e.ptyActive.Store(true)

	// Put the real terminal in raw mode
	stdinFd := int(os.Stdin.Fd())
	if cleanup := setupTerminalRawMode(stdinFd, nil); cleanup != nil {
		defer cleanup()
	}

	// Handle terminal resize
	defer setupPTYResize(stdinFd, ptmx, nil)()

	drainTerminalResponses(stdinFd, nil)

	// Copy stdin to PTY
	done := make(chan struct{})
	stdinDone := make(chan struct{})
	var lastCtrlC time.Time
	onInput := func(buf []byte) {
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

	startStdinCopy(stdinCopyConfig{
		ptmx:      ptmx,
		stdin:     hc.Stdin,
		done:      done,
		stdinDone: stdinDone,
		onInput:   onInput,
		onWrite:   nil,
		ptyTr:     nil,
	})

	// Copy PTY to stdout
	stdoutDone := make(chan error, 1)
	go func() {
		err := copyWithRetry(hc.Stdout, ptmx, nil, nil)
		stdoutDone <- err
	}()

	// Wait for command
	cmdDone := make(chan error, 1)
	go func() {
		cmdDone <- cmd.Wait()
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
			signalProcessGroup(cmd, syscall.SIGTERM)
			cmdErr = <-cmdDone
			if cmdErr == nil && stdoutErr != nil {
				cmdErr = stdoutErr
			}
		}
	}

	close(done)
	<-stdinDone

	if !stdoutFinished {
		select {
		case stdoutErr = <-stdoutDone:
		case <-time.After(ptyStopGrace):
			_ = ptmx.Close()
			<-stdoutDone
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
	prog, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader("cd "+shellQuote(cwd)), "")
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
