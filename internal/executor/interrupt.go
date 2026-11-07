package executor

import (
	"os"
	"sync"
	"syscall"
	"time"
)

// lateSIGINT bounds how late the shell's own SIGINT may come after the end
// of the commands it reached with: Ctrl+C sends it to the shell and its
// foreground commands at once, but Go hands the shell its signal on
// another goroutine, so a command's end can be seen first. A last command
// that died of SIGINT waits this long for it (a command that kills itself
// with SIGINT, no Ctrl+C, costs the script the wait), and a SIGINT this
// soon after commands that ended otherwise is theirs: they handled it. A
// SIGINT later still is taken as a new one, for whatever runs then (a
// Ctrl+C typed that soon after a command ended is taken as the command's).
const lateSIGINT = 100 * time.Millisecond

// foreground is the external commands the shell waits for, for a SIGINT
// that reaches a line (InterruptLine). Ctrl+C at a terminal sends SIGINT to
// the whole foreground process group: the shell and the commands it waits
// for. As in bash, the commands decide, once the last of them ended: the
// shell ends when they died of it, and goes on when one handled it.
type foreground struct {
	mu sync.Mutex
	b  batch // the batch running; the zero batch (idle) when none does
	// watched: the shell's SIGINT comes to interrupt (WatchInterrupts), so
	// a command's SIGINT death may have one on its way. Unwatched, none
	// comes, and nothing waits for it.
	watched bool
}

// batch is the foreground commands that run together, one command or the
// commands of a pipeline: it starts with the first and ends with the last,
// where it decides what a SIGINT does. Its fields go with it.
type batch struct {
	state   batchState
	running int       // its commands running
	ended   time.Time // batchSettling: when its last command ended
	// caught: since the SIGINT, a command ended other than by it (it
	// handled the signal). Only a batch that saw a SIGINT sets it.
	caught bool
	// end ends the shell; set by the SIGINT that interrupted the batch.
	end func()
	// arrived is closed when the batch is interrupted, for its last
	// command's wait for a late SIGINT (batchSIGINTDeathSeen).
	arrived chan struct{}
}

// batchState is where a batch stands with a SIGINT.
type batchState int

const (
	batchIdle    batchState = iota // no foreground command runs
	batchRunning                   // its commands run; no SIGINT so far
	// batchSIGINTDeathSeen: a command died of SIGINT before the shell took
	// its own, which is on its way (Ctrl+C), or never comes (the command
	// killed itself).
	batchSIGINTDeathSeen
	batchInterrupted // the shell took a SIGINT while the batch ran
	// batchSettling: the commands ended, with no SIGINT seen, a moment ago
	// (ended): a SIGINT that lands now is theirs, which they handled
	// (lateSIGINT). Past that the shell is idle; a command starting starts
	// a new batch.
	batchSettling
)

// start records a foreground command that started: the first starts a
// batch, the others join it.
func (f *foreground) start() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.b.state == batchIdle || f.b.state == batchSettling {
		f.b = batch{state: batchRunning, arrived: make(chan struct{})}
	}
	f.b.running++
}

// end records that a foreground command ended, as ps says. The batch's
// last command decides: the shell ends when the batch was interrupted and
// no command of it handled the SIGINT; handled reports that one did. A
// last command that died of a SIGINT the shell has not taken yet waits a
// moment for it (lateSIGINT), when the shell's SIGINT is watched.
func (f *foreground) end(ps *os.ProcessState) (handled bool) {
	f.mu.Lock()
	f.b.note(diedOf(ps, syscall.SIGINT))
	if f.b.running == 0 && f.b.state == batchSIGINTDeathSeen && f.watched {
		arrived := f.b.arrived
		f.mu.Unlock()
		select {
		case <-arrived:
		case <-time.After(lateSIGINT):
		}
		f.mu.Lock()
	}
	if f.b.running > 0 { // the batch goes on: a later end decides
		f.mu.Unlock()
		return false
	}
	end, handled := f.decideLocked()
	f.mu.Unlock()
	if end != nil {
		end()
	}
	return handled
}

// note records that one of the batch's commands ended, died of SIGINT or
// not.
func (b *batch) note(died bool) {
	switch {
	case died && b.state == batchRunning:
		b.state = batchSIGINTDeathSeen
	case !died && b.state != batchRunning:
		b.caught = true
	}
	b.running--
}

// decideLocked ends the batch, whose last command ended, and returns the
// shell's end when the batch was interrupted and no command of it handled
// the SIGINT; nil when the shell goes on, with handled true when a command
// handled it. Call it with f.mu held.
func (f *foreground) decideLocked() (end func(), handled bool) {
	b := f.b
	f.b = batch{}
	switch {
	case b.state == batchInterrupted && !b.caught:
		return b.end, false
	case b.state == batchInterrupted:
		return nil, true
	case b.state == batchRunning && f.watched:
		f.b = batch{state: batchSettling, ended: time.Now()}
	}
	return nil, false
}

// interrupt takes a SIGINT: false when no foreground command runs (the
// shell ends now); true when the batch running decides, calling end when
// its commands died of it, or when the batch that just ended (settling)
// handled it, which handled then reports.
func (f *foreground) interrupt(end func()) (taken, handled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch f.b.state {
	case batchIdle:
		return false, false
	case batchSettling:
		late := time.Since(f.b.ended) < lateSIGINT
		f.b = batch{}
		return late, late
	}
	f.b.end = end
	if f.b.state != batchInterrupted {
		f.b.state = batchInterrupted
		close(f.b.arrived)
	}
	return true, false
}

// watch records that the shell's SIGINT comes to interrupt.
func (f *foreground) watch() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.watched = true
}

// state is where the batch stands, for tests.
func (f *foreground) state() batchState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.b.state
}

// diedOf reports whether ps is a process that sig killed.
func diedOf(ps *os.ProcessState, sig syscall.Signal) bool {
	if ps == nil {
		return false
	}
	ws, ok := ps.Sys().(syscall.WaitStatus)
	return ok && ws.Signaled() && ws.Signal() == sig
}

// InterruptLine tells the executor that the shell got SIGINT while a line
// runs (Ctrl+C at the terminal, which sends it to the foreground commands
// too). When a foreground command runs it reports true and leaves the
// decision to the commands running: end is called once they all ended, if
// they died of the signal; one that handled it lets the line go on, as in
// bash. With none running (the line runs in hash: a loop, a read, a wait
// between commands) it reports false: the caller ends the line now, by
// canceling its context. end ends the line the same way: a script ends,
// the interactive shell cancels the line.
func (e *Executor) InterruptLine(end func()) bool {
	taken, handled := e.foreground.interrupt(end)
	if handled {
		e.owed.owe(syscall.SIGINT)
	}
	return taken
}

// endForeground records that a foreground command ended, as ps says (see
// foreground.end): a batch that handled a SIGINT leaves the line owing the
// INT trap (owedTraps). A command that died of a signal the shell traps
// and is told of waits a moment (lateSIGINT, as for a SIGINT) for the
// shell's own copy (lateSignalOf), so its trap runs once this line ends.
// A command that killed itself costs the wait.
func (e *Executor) endForeground(ps *os.ProcessState) {
	if e.foreground.end(ps) {
		e.owed.owe(syscall.SIGINT)
	}
	if sig, late := e.lateSignalOf(ps); late {
		e.owed.await(sig, lateSIGINT)
	}
}

// WatchInterrupts tells the executor that its caller routes the shell's
// SIGINT to InterruptLine, from now on: a script's watchInterrupts, the
// interactive shell's claim of each line (UseInteractiveTerminal). A
// foreground command that dies of SIGINT then waits a moment for the
// shell's own SIGINT (lateSIGINT); in an executor whose SIGINT nobody
// routes it does not.
func (e *Executor) WatchInterrupts() {
	e.foreground.watch()
}
