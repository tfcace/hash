package executor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/tfcace/hash/internal/executor/jobexec"
	"mvdan.cc/sh/v3/interp"
)

// commandStopGrace is how long a command whose line was canceled has to
// end after its signal before it is killed: the longest a fatal signal
// waits for the command running, where bash waits for it to end. A
// variable for tests.
var commandStopGrace = 2 * time.Second

// StopSignal is the signal for a command whose line ctx ended: the fatal
// signal that ended hash when the cause names one (SignalCause), SIGTERM
// for any other end. A line the shell's ending cut off ends with 128 plus
// it.
func StopSignal(ctx context.Context) syscall.Signal {
	if sig, ok := SignalCause(ctx); ok {
		return sig
	}
	return syscall.SIGTERM
}

// SignalCause is the signal ctx's cause names (a Signal method), if it
// does: the fatal signal that ended hash. It is the one reading of a fatal
// signal off a context, for the executor, the shell and cmd/hash.
func SignalCause(ctx context.Context) (syscall.Signal, bool) {
	var named interface{ Signal() syscall.Signal }
	if errors.As(context.Cause(ctx), &named) {
		return named.Signal(), true
	}
	return 0, false
}

// stopWhenCanceled watches a started command: when ctx ends first, send
// sends it StopSignal, as it would get from the terminal or the kill that
// ended hash, so it can clean up, and SIGKILL if it is still running
// commandStopGrace later. bash lets a running command finish instead;
// hash ends promptly because its background jobs are goroutines that
// cannot outlive it. Call release once the command's Wait returned.
func stopWhenCanceled(ctx context.Context, send func(syscall.Signal)) (release func()) {
	done := make(chan struct{})
	grace := commandStopGrace
	stop := context.AfterFunc(ctx, func() {
		send(StopSignal(ctx))
		select {
		case <-done:
		case <-time.After(grace):
			send(syscall.SIGKILL)
		}
	})
	return func() {
		close(done)
		stop()
	}
}

// jobGroup is the process group a job's command runs in. The interactive
// shell's leads one of its own, as a job of bash with job control: the
// terminal's Ctrl+C, Ctrl+\ and its hangup as hash exits reach only the
// shell's foreground, `kill 0` in it stays in it, and kill %N and the
// shell's hangup reach the processes it started. A script's shares the
// script's, as a job of bash with no job control: its hangup reaches it,
// and a process it starts that resets SIGTTOU (the children of macOS's
// /bin/sh, bash 3.2) can set the terminal up, where in a background group
// the terminal would stop it for good.
func (e *Executor) jobGroup() jobexec.Group {
	if e.terminal == terminalInteractive {
		return jobexec.OwnGroup
	}
	return jobexec.ShellGroup
}

// selfBinary is the identity of hash's own executable as hash started: its
// file's device, inode and modification time. A job starts through the
// executable at hash's path only while that file is still the one (runs):
// an upgrade or a downgrade that replaced it may not start a job's command
// as jobexec does.
type selfBinary struct {
	known    bool
	dev, ino uint64
	mtime    int64
}

// binaryAt is the identity of the file at path; not known when it cannot
// be read.
func binaryAt(path string) selfBinary {
	if path == "" {
		return selfBinary{}
	}
	info, err := os.Stat(path)
	if err != nil {
		return selfBinary{}
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return selfBinary{}
	}
	return selfBinary{known: true, dev: uint64(st.Dev), ino: st.Ino, mtime: info.ModTime().UnixNano()} //nolint:gosec,unconvert // G115: a device number, int32 on darwin
}

// runs reports whether the file at path is still hash's own executable.
func (s selfBinary) runs(path string) bool {
	return s.known && binaryAt(path) == s
}

// commandStartedKey is the context value of a foreground command's start
// hook: a func() the start of the command calls (commandStarted).
type commandStartedKey struct{}

// withCommandStarted is ctx with started called as the foreground command
// run on it starts: an exec's, whose start replaces the shell.
func withCommandStarted(ctx context.Context, started func()) context.Context {
	return context.WithValue(ctx, commandStartedKey{}, started)
}

// commandStarted calls ctx's start hook, if it has one, once a foreground
// command started.
func commandStarted(ctx context.Context) {
	if started, ok := ctx.Value(commandStartedKey{}).(func()); ok {
		started()
	}
}

// runCommand runs a foreground cmd to its end, stopping it when ctx ends
// first (see stopWhenCanceled). It is counted in the foreground while it
// runs, for a SIGINT (Executor.InterruptLine).
func (e *Executor) runCommand(ctx context.Context, cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return &startError{err}
	}
	commandStarted(ctx)
	release := stopWhenCanceled(ctx, func(sig syscall.Signal) { _ = cmd.Process.Signal(sig) })
	defer release()
	e.foreground.start()
	err := cmd.Wait()
	e.endForeground(cmd.ProcessState)
	return err
}

// runJob runs a background job's command to its end, started through hash
// itself with the signals of its process group ignored (jobGroup, see
// jobexec); started as it is, with no ignore, when hash's own binary is
// not the one that runs (selfBinary: an upgrade replaced it). A start
// failure of the command itself comes from the started hash, which says
// why and exits 126.
// It writes to the terminal (no tostop); its stdin is never the terminal,
// see detachedStdin. Its line's context ending does not stop it: a shell
// leaves it running when it ends, whatever ends it, as bash does, and the
// interactive shell a fatal signal ends hands it the hangup (SignalJobs).
// kill stops it (job.kill), a command too that starts as kill comes.
func (e *Executor) runJob(ctx context.Context, cmd *exec.Cmd) error {
	j := jobFrom(ctx)
	if sig := j.killedBy(); sig != 0 {
		return interp.ExitStatus(128 + uint8(sig)) //nolint:gosec // G115: a signal number
	}
	group := e.jobGroup()
	if group == jobexec.OwnGroup {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if e.self.runs(e.shellPath) {
		cmd.Args = jobexec.Args(e.shellPath, group, cmd.Path, cmd.Args)
		cmd.Path = e.shellPath
	}
	if err := cmd.Start(); err != nil {
		return &startError{err}
	}
	if sig := j.startCommand(cmd); sig != 0 {
		_ = cmd.Process.Signal(sig)
	}
	defer j.endCommand(cmd)
	return cmd.Wait()
}
