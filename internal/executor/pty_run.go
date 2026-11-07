package executor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
	"mvdan.cc/sh/v3/interp"
)

// runWithPTY runs a command whose stdin and stdout are the terminal on a
// pseudo-terminal of its own: the PTY is its controlling terminal, in a
// session of its own that hash itself leads (startOnPTY), and its stdin,
// stdout and stderr (streams the command line sends elsewhere, `2> log`,
// keep their file). The real terminal goes raw and hash copies its keys to
// the PTY (the keys an earlier PTY command of the line left unread, then
// the typeahead, without the terminal's replies), so Ctrl+C and
// full-screen input work; the keys the command leaves unread come back
// when it ends (lineRun.unread). The terminal's Ctrl+C reaches the command
// alone, never hash, so a death of it ends the line from here.
func (e *Executor) runWithPTY(ctx context.Context, cmd *exec.Cmd, hc interp.HandlerContext, pc pipelineContext) error {
	if pc.stderrTerm == nil {
		cmd.Stderr = hc.Stderr // redirected or piped
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
	cmd.SysProcAttr.Setctty = true
	// A stream hash copies (stderr to a writer that is no file) ends with
	// the command, not once a background child it left holding it does.
	cmd.WaitDelay = ptyStopGrace
	ptmx, report, err := e.startOnPTY(cmd)
	switch {
	case err != nil && cmd.Process == nil && isExecFailure(err):
		return &startError{err}
	case err != nil:
		// No PTY to be had: the command runs on the shell's streams, in
		// the shell's session. (A failed open leaves cmd unstarted.)
		cmd.SysProcAttr = nil
		cmd.Stdin = hc.Stdin
		cmd.Stdout = hc.Stdout
		cmd.Stderr = hc.Stderr
		return e.runCommand(ctx, cmd)
	}
	if startErr := report.started(); startErr != nil {
		// The leader could not start the command, or ended without a
		// word: it never ran, and an exec of it does not end the shell
		// (commandStarted). A silent leader's status is the command's.
		waitErr := cmd.Wait()
		_ = ptmx.Close()
		if errors.Is(startErr, errLeaderSilent) {
			return normalizeExternalCommandError(waitErr)
		}
		return &startError{startErr}
	}
	commandStarted(ctx)
	e.foreground.start() // ended once cmd's Wait returned (ptyRun.awaitExit)
	r := &ptyRun{e: e, cmd: cmd, ptmx: ptmx, ptmxFD: nonblockingFD(ptmx), report: report, lr: lineRunFrom(ctx)}
	r.group = r.foregroundGroup()
	defer stopWhenCanceled(ctx, r.signal)()
	r.trace = newPTYTrace(cmd, ptmx)
	defer r.trace.Close()

	// The child handles its own display: hide the progress bar meanwhile.
	if r.lr != nil {
		r.lr.ranPTY = true
		r.lr.status.drawStarted()
		defer r.lr.status.drawEnded()
	}
	stdinFd := int(r.e.input().Fd())
	if restore := setupTerminalRawMode(stdinFd, r.trace); restore != nil {
		defer restore()
	}
	defer setupPTYResize(stdinFd, r.ptmxFD, r.trace)()

	// Drop the replies to terminal queries still on their way before the
	// key copy hands the terminal's input to the command; the keys typed
	// ahead are the command's, as the terminal would give them to it. (A
	// command that does not read the terminal leaves them there for the
	// shell, or for a pager that reads the terminal itself.)
	typedAhead := append(r.lr.takeUnread(), drainTerminalReplies(stdinFd, r.trace)...)
	stdin, _ := hc.Stdin.(*os.File) // the terminal (needsPTY)
	r.start(stdin, typedAhead, r.lr.watchOwner(hc.Stdout))
	return r.finish()
}

// ptyPhase is where the run of a PTY command is (ptyRun).
type ptyPhase int

const (
	// ptyRunning: the command runs, its keys and output are copied. Once
	// the leader says it ended, the key copy stops and then releases the
	// leader to read the keys left on the PTY (keyCopy, ptyReport.atEnd).
	ptyRunning ptyPhase = iota
	// ptyExited: the leader exited (cmd's Wait returned), after its
	// report; the key copy stopped, the keys it held are in held.
	ptyExited
	// ptyDrained: the output copy ended, the PTY is closed and the keys
	// the command did not read are the line's: the run is over.
	ptyDrained
)

// ptyRun is the run of a command on a PTY, from its start to its end, in
// the order of its phases: the key copy stops (holding the keys the PTY
// had no room for) and only then releases the leader, which reads the keys
// left on the PTY and exits; then the output is drained, and the PTY
// closed. Only finish moves the phase.
type ptyRun struct {
	e      *Executor
	cmd    *exec.Cmd // the leader (or the command, leading its PTY itself)
	ptmx   *os.File
	ptmxFD int // ptmx's descriptor, non-blocking
	report *ptyReport
	lr     *lineRun
	trace  *ptyTrace

	phase   ptyPhase
	keys    *keyCopy
	output  *outputCopy
	exited  chan error // cmd's Wait
	exitErr error      // ptyExited: what cmd's Wait returned

	group  int          // the command's process group, the PTY's foreground as it started
	ctrlCs ctrlCPresses // the key copy's
	held   []byte       // ptyExited: the keys the copy read and the PTY had no room for
}

// start starts the run: the key copy from the terminal stdin (after
// typedAhead), the output copy to stdout, and the wait for the leader.
func (r *ptyRun) start(stdin *os.File, typedAhead []byte, stdout io.Writer) {
	r.keys = startKeyCopy(r.ptmxFD, stdin, typedAhead, r.report.releaser(), keyHooks{
		onInput: r.onKeys,
		onWrite: r.trace.markInWrite,
		ended:   func(err error) { r.trace.logf("stdin->pty copy stopped: %s", describeCopyEnd(err)) },
	})
	// The keys typed once the command ended are the next prompt's.
	r.report.atEnd(func() { r.keys.stop() })
	r.output = startOutputCopy(stdout, r.ptmxFD, r.trace.markOutRead, r.trace.markOutWrite)
	r.exited = make(chan error, 1)
	go func() {
		err := r.cmd.Wait()
		if errors.Is(err, exec.ErrWaitDelay) {
			err = nil // the command succeeded; a child it left holds its stream
		}
		if diedOf(r.cmd.ProcessState, syscall.SIGINT) && r.lr != nil {
			// Ctrl+C went to the command's own terminal, not to hash: it
			// interrupts the line as the shell's SIGINT would, the way bash
			// with job control takes a job's death of SIGINT as its own,
			// and the command's death decides with it (a command of the
			// batch that handled it lets the line go on).
			r.e.InterruptLine(r.lr.interrupt)
		}
		r.e.endForeground(r.cmd.ProcessState)
		r.trace.logf("cmd.Wait returned err=%v", err)
		r.exited <- err
	}()
}

// foregroundGroup is the PTY's foreground process group: the command's,
// as it starts (the leader made it so before the command's exec); 0 when
// the PTY does not say.
func (r *ptyRun) foregroundGroup() int {
	group, err := unix.IoctlGetInt(r.ptmxFD, unix.TIOCGPGRP)
	if err != nil {
		return 0
	}
	return group
}

// signal sends sig to the leader's process group: the leader passes the
// signals it can catch on to the command's group (ptyexec). SIGKILL, which
// it cannot, goes to the command's group as well.
func (r *ptyRun) signal(sig syscall.Signal) {
	signalProcessGroup(r.cmd, sig)
	if sig == syscall.SIGKILL && r.group > 0 && r.group != r.cmd.Process.Pid {
		_ = syscall.Kill(-r.group, sig)
	}
}

// onKeys sees the keys the copy read: a second Ctrl+C within
// doubleCtrlCWait of the first sends the command's group SIGINT itself,
// for a program that ignores its terminal's.
func (r *ptyRun) onKeys(buf []byte) {
	r.trace.markInRead(len(buf))
	for _, b := range buf {
		if b != ctrlCByte {
			continue
		}
		if r.ctrlCs.press(time.Now()) {
			r.signal(syscall.SIGINT)
		}
	}
}

// ctrlCPresses is the Ctrl+Cs a PTY command's key copy read: a second
// within doubleCtrlCWait of the first sends the command's group SIGINT.
type ctrlCPresses struct {
	last time.Time // the last press
	sent bool      // a double press sent the group SIGINT
}

// press records a Ctrl+C read at now and reports whether it is the second
// of a double one, which sends SIGINT.
func (p *ctrlCPresses) press(now time.Time) bool {
	double := !p.last.IsZero() && now.Sub(p.last) <= doubleCtrlCWait
	p.last = now
	p.sent = p.sent || double
	return double
}

// endedIt reports whether the SIGINT the presses sent ended cmd, which
// ended: it died of it, or handled it and exited 130.
func (p *ctrlCPresses) endedIt(cmd *exec.Cmd) bool {
	return p.sent && (diedOf(cmd.ProcessState, syscall.SIGINT) || cmd.ProcessState.ExitCode() == 128+int(syscall.SIGINT))
}

// finish waits the run out through its phases and returns the command's
// status, keeping the keys it left unread for the line.
func (r *ptyRun) finish() error {
	for r.phase != ptyDrained {
		switch r.phase {
		case ptyRunning:
			r.awaitExit()
		case ptyExited:
			r.drain()
		}
	}
	left, held := r.report.wait(), r.held
	if r.ctrlCs.endedIt(r.cmd) {
		// The Ctrl+Cs a program with its terminal raw never read ended it:
		// they are not the next prompt's.
		left, held = withoutCtrlC(left), withoutCtrlC(held)
	}
	r.lr.keepUnread(left) // the keys the command left on the PTY,
	r.lr.keepUnread(held) // then the ones the PTY had no room for
	// A failed output copy fails a command that succeeded; output cut off
	// after its end does not.
	if outErr := r.output.wait(); r.exitErr == nil && !errors.Is(outErr, errCopyDone) {
		return outErr
	}
	return normalizeExternalCommandError(r.exitErr)
}

// awaitExit waits for the leader to exit (ptyRunning to ptyExited) and
// stops the key copy, if the leader's end did not (a command leading its
// PTY, a leader lost). Output that ends first (the command closed the
// PTY's terminal) gives it ptyStopGrace to exit, then its group SIGTERM.
func (r *ptyRun) awaitExit() {
	var err error
	select {
	case err = <-r.exited:
	case <-r.output.done:
		select {
		case err = <-r.exited:
		case <-time.After(ptyStopGrace):
			r.trace.logf("pty->stdout ended early; sending SIGTERM to process group")
			r.signal(syscall.SIGTERM)
			err = <-r.exited
		}
	}
	r.exitErr = err
	r.held = r.keys.stop()
	r.phase = ptyExited
}

// drain lets the output copy end once the leader exited (ptyExited to
// ptyDrained): the PTY's terminal ends with the leader, unless a process
// the command left holds it, whose output the copy then reads until the
// PTY was quiet for ptyStopGrace, or for ptyDrainCap at the most
// (outputCopy). The PTY is closed after.
func (r *ptyRun) drain() {
	r.output.end()
	r.trace.logf("pty->stdout copy stopped: %s", describeCopyEnd(r.output.wait()))
	_ = r.ptmx.Close()
	r.phase = ptyDrained
}

// withoutCtrlC is keys without the Ctrl+Cs in them.
func withoutCtrlC(keys []byte) []byte {
	return bytes.ReplaceAll(keys, []byte{ctrlCByte}, nil)
}

// setupTerminalRawMode puts the terminal in raw mode and returns the
// function that restores it; nil when it is no terminal or cannot be.
func setupTerminalRawMode(stdinFd int, ptyTr *ptyTrace) func() {
	ptyTr.logf("terminal stdin fd=%d is_tty=%t", stdinFd, term.IsTerminal(stdinFd))
	if !term.IsTerminal(stdinFd) {
		return nil
	}
	oldState, err := term.MakeRaw(stdinFd)
	if err != nil {
		return nil
	}
	return func() { _ = term.Restore(stdinFd, oldState) }
}

// setupPTYResize gives the PTY whose master is ptmxFD the terminal's size,
// now and at each SIGWINCH, and returns the function that stops that.
// (pty.Setsize would put a pollable master back in blocking mode.)
func setupPTYResize(stdinFd, ptmxFD int, ptyTr *ptyTrace) func() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	resize := func() {
		if ws, err := unix.IoctlGetWinsize(stdinFd, unix.TIOCGWINSZ); err == nil {
			_ = unix.IoctlSetWinsize(ptmxFD, unix.TIOCSWINSZ, ws)
			ptyTr.logf("pty size set rows=%d cols=%d", ws.Row, ws.Col)
		}
	}
	resize()
	go func() {
		for range sigCh {
			resize()
		}
	}()
	return func() {
		signal.Stop(sigCh)
		close(sigCh)
	}
}
