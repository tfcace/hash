package executor

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/tfcace/hash/internal/programstatus"
	"github.com/tfcace/hash/internal/progress"
)

// cmdStatus is everything hash shows the terminal about one running line,
// with one owner so a timer and the line's end cannot race:
//
//   - the cmd record (OSC 7501): working once the line runs past the
//     progress threshold, replaced by done or error when it ends. The record
//     lives under an id so a child that reports on the root record is never
//     overwritten; a terminal shows the most urgent record and prefers the
//     root, so hash's only shows when the child says nothing. A child that
//     owns its status takes the record back (withdraw): one that enters the
//     alternate screen is a full-screen program, not a job, and one that
//     sends its own report speaks for itself.
//   - the progress bar (OSC 9;4): shown past the threshold while no
//     command draws on the terminal. One that does (a PTY child: vim, a
//     TUI, a colored make; a pipeline's tail, `ls | less`, which writes to
//     the terminal itself) handles its own display; once it ends, the bar
//     waits a threshold of its own (bar, barTimer) before it returns, so a
//     line that ends with one does not flash it. The record keeps the
//     line's own threshold.
//
// The record and the bar are written to the terminal from the timers, with
// no ordering against the child's output: a write can land between two
// writes of the child that split an escape sequence. Preventing that takes a
// tracker of the child's escape state and a flush timeout for a child stuck
// mid-sequence; it is not done, and the commands that draw, which write
// the most escapes, already keep the bar off.
type cmdStatus struct {
	mu        sync.Mutex
	r         *programstatus.Reporter
	osc       *progress.OSC
	line      string
	threshold time.Duration
	timer     *time.Timer // the line's threshold
	barTimer  *time.Timer // the bar's threshold after a drawing command; made when the first one ends

	phase     linePhase
	bar       barWait
	app       string // basename of the external command started last
	withdrawn bool   // the child owns its record; say nothing more
	drawing   int    // commands drawing on the terminal; the bar is hidden meanwhile
}

// barWait is which threshold the progress bar waits for.
type barWait int

const (
	barWaitsForLine barWait = iota // the line's threshold: no drawing command has ended
	barWaitsForDraw                // barTimer's, restarted when the last drawing command ended
	barDue                         // passed: the bar shows while the line runs and no command draws
)

// linePhase is where a line is relative to the progress threshold.
type linePhase int

const (
	linePending linePhase = iota // under the threshold: nothing shown
	lineRunning                  // past the threshold: the record is working
	lineOver                     // the line ended
)

// newCmdStatus starts the threshold timer for a line.
func newCmdStatus(r *programstatus.Reporter, osc *progress.OSC, line string, threshold time.Duration) *cmdStatus {
	c := &cmdStatus{r: r, osc: osc, line: line, threshold: threshold}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timer = time.AfterFunc(threshold, c.thresholdPassed)
	return c
}

func (c *cmdStatus) recordVisible() bool { return c.phase == lineRunning && !c.withdrawn }

func (c *cmdStatus) progressVisible() bool {
	return c.phase == lineRunning && c.drawing == 0 && c.bar == barDue
}

// change applies fn to the state and shows or removes the bar to match.
// Must be called with mu held.
func (c *cmdStatus) change(fn func()) {
	was := c.progressVisible()
	fn()
	switch now := c.progressVisible(); {
	case now && !was:
		c.osc.Start()
	case was && !now:
		c.osc.Done()
	}
}

// thresholdPassed reports working for the line, with the program's name
// when one ran, and shows the bar unless a command draws on the terminal
// or has restarted the bar's wait.
func (c *cmdStatus) thresholdPassed() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.phase != linePending {
		return
	}
	c.change(func() {
		c.phase = lineRunning
		if !c.withdrawn {
			c.r.Report(programstatus.Report{State: programstatus.Working, ID: "cmd", App: c.app, Msg: c.line})
		}
		if c.bar == barWaitsForLine {
			c.bar = barDue
		}
	})
}

// barThresholdPassed shows the bar a threshold after the last drawing
// command ended, once the line itself is past its threshold.
func (c *cmdStatus) barThresholdPassed() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.phase == lineOver || c.drawing > 0 || c.bar != barWaitsForDraw {
		return
	}
	c.change(func() { c.bar = barDue })
}

// setProgram records the external command the line started last.
func (c *cmdStatus) setProgram(app string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.app = app
}

// drawStarted hides the bar while a command draws on the terminal.
func (c *cmdStatus) drawStarted() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.change(func() { c.drawing++ })
}

// drawEnded restarts the bar's threshold once no command draws.
func (c *cmdStatus) drawEnded() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.change(func() {
		c.drawing--
		if c.drawing > 0 || c.phase == lineOver {
			return
		}
		c.bar = barWaitsForDraw
		if c.barTimer == nil {
			c.barTimer = time.AfterFunc(c.threshold, c.barThresholdPassed)
		} else {
			c.barTimer.Reset(c.threshold)
		}
	})
}

// withdraw hands the record to the child: what hash reported is cleared,
// and nothing more is reported for the line.
func (c *cmdStatus) withdraw() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.recordVisible() {
		c.r.Report(programstatus.Report{State: programstatus.Clear, ID: "cmd"})
	}
	c.withdrawn = true
}

// finish ends the line with exitCode: done for 0, otherwise error with the
// status in the message. A line that never reported working stays silent.
func (c *cmdStatus) finish(exitCode int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timer.Stop()
	if c.barTimer != nil {
		c.barTimer.Stop()
	}
	if c.recordVisible() {
		rep := programstatus.Report{State: programstatus.Done, ID: "cmd", App: c.app, Msg: c.line}
		if exitCode != 0 {
			rep.State = programstatus.Error
			rep.Msg = fmt.Sprintf("%s (exit %d)", c.line, exitCode)
		}
		c.r.Report(rep)
	}
	c.change(func() { c.phase = lineOver })
}

// ownerPatterns are the signs, in a child's output, that it owns its
// terminal status: the alternate screen (DECSET 1049, 1047 or 47), or a
// Program Status report of its own.
var ownerPatterns = [][]byte{
	[]byte("\x1b[?1049h"),
	[]byte("\x1b[?1047h"),
	[]byte("\x1b[?47h"),
	[]byte("\x1b]7501;"),
}

// ownerTail is the longest pattern less one byte: a pattern that straddles
// two writes is found by keeping this much of the previous one.
const ownerTail = 7

// ownerScanner passes a child's output through and calls hit, once, when
// the output shows the child owns its status.
type ownerScanner struct {
	w    io.Writer
	hit  func()
	tail []byte
	done bool
}

func newOwnerScanner(w io.Writer, hit func()) *ownerScanner {
	return &ownerScanner{w: w, hit: hit}
}

func (s *ownerScanner) Write(p []byte) (int, error) {
	if !s.done {
		s.scan(p)
	}
	return s.w.Write(p)
}

func (s *ownerScanner) scan(p []byte) {
	// The tail of the last write joined to the head of this one catches a
	// pattern split between them; the write itself catches the rest.
	head := p
	if len(head) > ownerTail {
		head = head[:ownerTail]
	}
	joined := append(append([]byte(nil), s.tail...), head...)
	for _, pat := range ownerPatterns {
		if bytes.Contains(joined, pat) || bytes.Contains(p, pat) {
			s.done = true
			s.tail = nil
			s.hit()
			return
		}
	}
	if len(p) >= ownerTail {
		s.tail = append(s.tail[:0], p[len(p)-ownerTail:]...)
	} else {
		s.tail = append(s.tail, p...)
		if len(s.tail) > ownerTail {
			s.tail = s.tail[len(s.tail)-ownerTail:]
		}
	}
}

// watchOwner wraps a PTY child's output so the line's record is withdrawn
// as soon as the child shows it owns its status.
func (lr *lineRun) watchOwner(w io.Writer) io.Writer {
	if lr == nil {
		return w
	}
	return newOwnerScanner(w, lr.status.withdraw)
}
