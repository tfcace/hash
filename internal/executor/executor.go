package executor

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
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

// maxCaptureSize limits how much output we capture (1MB).
const maxCaptureSize = 1024 * 1024

// limitedWriter wraps a writer and stops writing after n bytes.
type limitedWriter struct {
	w io.Writer
	n int64
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.w.Write(p)
	l.n -= int64(n)
	return n, err
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
	positionalArgs    []string // $0, $1, $2, etc. for -c execution
	ptyActive         atomic.Bool // set when PTY is in use, disables progress
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
	limitedCapture := &limitedWriter{w: &captureBuf, n: maxCaptureSize}

	// Set up output writers with capture
	var actualStdout io.Writer = io.Discard
	if stdout != nil {
		actualStdout = io.MultiWriter(stdout, limitedCapture)
	} else {
		actualStdout = limitedCapture
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

	// Signal PTY is active to disable progress indicator
	e.ptyActive.Store(true)

	// Put the real terminal in raw mode so keystrokes are passed through
	// character-by-character to the PTY, rather than being line-buffered.
	// This is required for interactive programs like vim, helix, claude, etc.
	stdinFd := int(os.Stdin.Fd())
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
	}

	// Handle resize signals
	go func() {
		for range sigCh {
			if w, h, err := term.GetSize(stdinFd); err == nil {
				pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(h), Cols: uint16(w)})
			}
		}
	}()

	go io.Copy(ptmx, hc.Stdin)
	io.Copy(hc.Stdout, ptmx)

	return cmd.Wait()
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
