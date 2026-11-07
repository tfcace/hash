package executor

import (
	"errors"
	"io"
	"os"
	"os/exec"

	"github.com/creack/pty"
	"github.com/tfcace/hash/internal/executor/ptyexec"
)

// ptyReportState is where the report of a PTY command's session leader
// (ptyexec) is.
type ptyReportState int

const (
	leaderStarting     ptyReportState = iota // no word yet on the command's start
	commandRunning                           // the command started
	commandEnded                             // it ended: the leader awaits its release
	leaderReported                           // the leader reported the keys left on the PTY
	commandStartFailed                       // the command's exec failed: it never ran
	leaderSilent                             // the leader ended before saying the command started
	leaderLost                               // the leader ended after the start without its last word
)

// errLeaderSilent is started's error for a leader that ended without
// saying whether the command started (killed, or a hash of another
// build): hash knows of no command that ran.
var errLeaderSilent = errors.New("pty helper ended without a report")

// ptyReport is the report of a PTY command's session leader, read as it
// comes (read): started tells whether the command started, atEnd acts as
// the command ended, wait returns the rest once the leader ended. It holds
// the leader's release (releaser) for the copy of the keys to the PTY to
// close once it stopped. A leader's word out of turn
// (a misbehaving leader) is dropped. Only read moves state; the others
// wait for the milestone channels, which it closes once each.
type ptyReport struct {
	state    ptyReportState
	start    chan struct{} // closed once state left leaderStarting
	ended    chan struct{} // closed at commandEnded
	done     chan struct{} // closed at a final state: the leader ended
	release  *os.File      // closed to let the leader read the PTY's keys (releaser)
	left     []byte        // leaderReported: the keys the command left unread
	startErr error         // set before start closes: the command never ran
}

func newPTYReport(release *os.File) *ptyReport {
	return &ptyReport{start: make(chan struct{}), ended: make(chan struct{}), done: make(chan struct{}), release: release}
}

// read reads the leader's report from r to its end: the leader's ending.
func (r *ptyReport) read(src io.Reader, path string) {
	left, err := ptyexec.Report(src, path,
		func() { r.step(leaderStarting, commandRunning) },
		func() { r.step(commandRunning, commandEnded) })
	switch {
	case err == nil && r.state == commandEnded:
		r.left = left
		r.finish(leaderReported)
	case isExecFailure(err) && r.state == leaderStarting:
		r.startErr = err
		r.finish(commandStartFailed)
	case r.state == leaderStarting:
		r.startErr = errLeaderSilent
		r.finish(leaderSilent)
	default:
		r.finish(leaderLost)
	}
}

// step moves the report from the state from to next, as the leader's word
// does; a word that does not follow from the state is dropped.
func (r *ptyReport) step(from, next ptyReportState) {
	if r.state != from {
		return
	}
	r.state = next
	switch next {
	case commandRunning:
		close(r.start)
	case commandEnded:
		close(r.ended)
	}
}

// finish ends the report in the final state final.
func (r *ptyReport) finish(final ptyReportState) {
	if r.state == leaderStarting {
		close(r.start)
	}
	r.state = final
	close(r.done)
}

// atEnd calls f once the leader says the command ended; never for a nil
// report (no leader), nor for a leader that ends without saying it.
func (r *ptyReport) atEnd(f func()) {
	if r == nil {
		return
	}
	go func() {
		select {
		case <-r.ended:
		case <-r.done:
		}
		select {
		case <-r.ended: // closed before done, when at all
			f()
		default:
		}
	}()
}

// releaser is the leader's release: closing it lets the leader read the
// keys left on the PTY. Nil for a nil report.
func (r *ptyReport) releaser() io.Closer {
	if r == nil {
		return nil
	}
	return r.release
}

// wait waits for the report, which ends when the leader does, and returns
// the keys the command left unread; nil for a nil report (the command led
// its PTY itself) and for a leader that did not report them.
func (r *ptyReport) wait() (left []byte) {
	if r == nil {
		return nil
	}
	<-r.done
	return r.left
}

// started waits until the leader started the command or could not: nil
// once it runs (or with no leader to wait for: a nil report), else the
// command's exec failure, or errLeaderSilent.
func (r *ptyReport) started() error {
	if r == nil {
		return nil
	}
	<-r.start
	return r.startErr
}

// startOnPTY starts cmd on a new PTY, its controlling terminal, through
// hash itself as the PTY's session leader (ptyexec), so the keys typed
// while it runs that it leaves unread are hash's again once it ended. When
// the program at hash's path is no longer hash's own binary (selfBinary:
// an upgrade replaced it, or removed it) cmd is the leader itself, and
// reports nothing. On an error cmd is as it was, unstarted.
func (e *Executor) startOnPTY(cmd *exec.Cmd) (*os.File, *ptyReport, error) {
	if !e.self.runs(e.shellPath) {
		ptmx, err := pty.StartWithAttrs(cmd, nil, cmd.SysProcAttr)
		return ptmx, nil, err
	}
	direct := *cmd // copied before it starts: a Cmd starts once
	r, w, release, pipeErr := reportPipes()
	if pipeErr != nil {
		ptmx, err := pty.StartWithAttrs(cmd, nil, cmd.SysProcAttr)
		return ptmx, nil, err
	}
	path := cmd.Path
	cmd.Args = ptyexec.Args(e.shellPath, cmd.Path, cmd.Args)
	cmd.Path = e.shellPath
	cmd.ExtraFiles = []*os.File{w, release[0]}
	ptmx, err := pty.StartWithAttrs(cmd, nil, cmd.SysProcAttr)
	_ = w.Close() // the leader's copies are the ones it holds
	_ = release[0].Close()
	if err != nil {
		_ = r.Close()
		_ = release[1].Close()
		*cmd = direct
		if isExecFailure(err) { // hash's own exec
			ptmx, err = pty.StartWithAttrs(cmd, nil, cmd.SysProcAttr)
		}
		return ptmx, nil, err
	}
	rep := newPTYReport(release[1])
	go func() {
		defer r.Close()
		rep.read(r, path)
	}()
	return ptmx, rep, nil
}

// reportPipes opens the pipes a PTY command's leader reports on (r, w) and
// is released on (release: its read end, then its write end).
func reportPipes() (r, w *os.File, release [2]*os.File, err error) {
	if r, w, err = os.Pipe(); err != nil {
		return nil, nil, release, err
	}
	if release[0], release[1], err = os.Pipe(); err != nil {
		_ = r.Close()
		_ = w.Close()
		return nil, nil, release, err
	}
	return r, w, release, nil
}
