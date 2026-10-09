package executor

import (
	"bytes"
	"fmt"
	"io"
	"sync"

	"github.com/tfcace/hash/internal/programstatus"
)

// cmdStatus is the terminal's cmd record (OSC 7501) for one line: working
// once the line runs past the progress threshold, replaced by done or error
// when it ends. The record lives under an id so a child that reports on the
// root record is never overwritten; a terminal shows the most urgent record
// and prefers the root, so hash's only shows when the child says nothing.
//
// A child that owns its status takes the record back (withdraw): one that
// enters the alternate screen is a full-screen program, not a job, and one
// that sends its own report speaks for itself.
type cmdStatus struct {
	mu        sync.Mutex
	r         *programstatus.Reporter
	line      string
	app       string
	shown     bool // working was reported and not withdrawn
	over      bool // the line ended
	withdrawn bool // the child owns its status; say nothing more
}

func newCmdStatus(r *programstatus.Reporter, line string) *cmdStatus {
	return &cmdStatus{r: r, line: line}
}

// start reports working, with app as the program's name ("" for none),
// unless the line already ended or the child owns its status.
func (c *cmdStatus) start(app string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.over || c.withdrawn {
		return
	}
	c.app = app
	c.r.Report(programstatus.Report{State: programstatus.Working, ID: "cmd", App: app, Msg: c.line})
	c.shown = true
}

// withdraw hands the record to the child: what hash reported is cleared,
// and nothing more is reported for the line.
func (c *cmdStatus) withdraw() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.withdrawn = true
	if !c.shown {
		return
	}
	c.r.Report(programstatus.Report{State: programstatus.Clear, ID: "cmd"})
	c.shown = false
}

// finish ends the line with exitCode: done for 0, otherwise error with the
// status in the message. A line that never reported working stays silent.
func (c *cmdStatus) finish(exitCode int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.over = true
	if !c.shown {
		return
	}
	rep := programstatus.Report{State: programstatus.Done, ID: "cmd", App: c.app, Msg: c.line}
	if exitCode != 0 {
		rep.State = programstatus.Error
		rep.Msg = fmt.Sprintf("%s (exit %d)", c.line, exitCode)
	}
	c.r.Report(rep)
	c.shown = false
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
const ownerTail = 8

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
func (e *Executor) watchOwner(w io.Writer) io.Writer {
	return newOwnerScanner(w, func() {
		if cs := e.cmd.Load(); cs != nil {
			cs.withdraw()
		}
	})
}
