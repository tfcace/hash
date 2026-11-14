package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"weak"

	"mvdan.cc/sh/v3/interp"
)

// The interpreter fires a runner's EXIT trap whenever it finishes running a
// file. Execute runs each line as one, so a trap set at the shell's top
// level would fire after every line, and runNested runs a program as one
// on a subshell's runner, so a subshell's would fire when that program
// ends. And it knows no signal traps: `trap cleanup INT TERM` fails there.
// Hash keeps the EXIT trap and the traps of the signals that end a shell
// (HUP, INT, QUIT, TERM) itself instead (shellTraps), and the interpreter
// never sees them. The shell's run when the shell ends: the trap of the
// signal that ends it (RunSignalTrap), then the EXIT trap (RunExitTrap),
// each once. A subshell's are listed but never run: the interpreter has no
// hook at a subshell's end and runs subshells in hash's process (README,
// bash differences). Hash lists the traps itself too (trap, trap -p).

// trapSignals are the signals hash keeps traps for, in listing order.
var trapSignals = []struct {
	name string
	sig  syscall.Signal
}{
	{"HUP", syscall.SIGHUP},
	{"INT", syscall.SIGINT},
	{"QUIT", syscall.SIGQUIT},
	{"TERM", syscall.SIGTERM},
}

// trapCondition is the condition hash keeps a trap for that spec names:
// EXIT (or 0), or one of trapSignals, by name in any case, with or without
// SIG, or by number, as bash reads them. ok is false for any other spec
// (ERR, a signal hash does not trap), which is the interpreter's.
func trapCondition(spec string) (cond string, ok bool) {
	name := strings.ToUpper(spec)
	if name == "EXIT" || name == "0" {
		return "EXIT", true
	}
	name = strings.TrimPrefix(name, "SIG")
	for _, ts := range trapSignals {
		if name == ts.name || name == strconv.Itoa(int(ts.sig)) {
			return ts.name, true
		}
	}
	return "", false
}

// signalCondition is the condition of a signal in trapSignals.
func signalCondition(sig syscall.Signal) (string, bool) {
	for _, ts := range trapSignals {
		if ts.sig == sig {
			return ts.name, true
		}
	}
	return "", false
}

// trapSet is one shell's traps that hash keeps, by condition. A condition
// missing from it has no trap; a trap with an empty action runs nothing
// (and so ignores its signal), and is listed.
type trapSet map[string]string

// trapPhase is how far the shell's end has got with its traps. The phases
// go in this order, each at most once.
type trapPhase int

const (
	trapsSet trapPhase = iota // the shell runs: its traps are set and listed
	// lineTrapRunning: a signal trap runs after a line, for a signal that
	// did not end the shell (owedTraps); the shell goes on after it
	// (trapsSet).
	lineTrapRunning
	signalTrapRunning // the trap of the signal that ends the shell runs
	signalTrapDone    // it ran: the EXIT trap is next
	exitTrapRunning   // the EXIT trap runs
	trapsDone         // the EXIT trap ran: no trap runs again
)

// shellTraps are the shell's traps, where its end is with them, and the
// traps of the subshells that set or reset their own. A subshell missing
// from subshells never ran a trap command: it lists the shell's, as in
// bash. An entry goes when its runner is garbage collected, so subshells
// that set traps do not pile up.
type shellTraps struct {
	mu        sync.Mutex
	shell     trapSet
	subshells map[weak.Pointer[interp.Runner]]trapSet
	phase     trapPhase
	// before is, while a trap runs, the status the shell had before it: a
	// bare exit in the trap exits with it (POSIX).
	before int
}

// set sets r's trap for cond to action, or removes it for action "-": the
// shell's when r is the shell's runner.
func (t *shellTraps) set(r, shell *interp.Runner, cond, action string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	traps := t.ofLocked(r, shell)
	if action == "-" {
		delete(traps, cond)
		return
	}
	traps[cond] = action
}

// ofLocked is r's own trap set, made on its first trap command.
func (t *shellTraps) ofLocked(r, shell *interp.Runner) trapSet {
	if r == shell {
		if t.shell == nil {
			t.shell = trapSet{}
		}
		return t.shell
	}
	if t.subshells == nil {
		t.subshells = make(map[weak.Pointer[interp.Runner]]trapSet)
	}
	key := weak.Make(r)
	traps, ok := t.subshells[key]
	if !ok {
		traps = trapSet{}
		t.subshells[key] = traps
		runtime.AddCleanup(r, t.drop, key)
	}
	return traps
}

func (t *shellTraps) drop(key weak.Pointer[interp.Runner]) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.subshells, key)
}

// of is a copy of r's own traps; own is false for a subshell that never
// ran a trap command.
func (t *shellTraps) of(r, shell *interp.Runner) (traps trapSet, own bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	src, own := t.shell, true
	if r != shell {
		src, own = t.subshells[weak.Make(r)]
	}
	traps = make(trapSet, len(src))
	for cond, action := range src {
		traps[cond] = action
	}
	return traps, own
}

// traps reports whether the shell has a trap for sig that runs something.
func (t *shellTraps) traps(sig syscall.Signal) bool {
	cond, ok := signalCondition(sig)
	if !ok {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.shell[cond] != ""
}

// ignores reports whether the shell's trap for sig is an empty one.
func (t *shellTraps) ignores(sig syscall.Signal) bool {
	cond, ok := signalCondition(sig)
	if !ok {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	action, set := t.shell[cond]
	return set && action == ""
}

// startSignalTrap starts the run of the shell's trap for sig, after a
// shell status of before. ok is false, and nothing starts, when the shell
// has no such trap or its end has started already.
func (t *shellTraps) startSignalTrap(sig syscall.Signal, before int) (action string, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	cond, _ := signalCondition(sig)
	action = t.shell[cond]
	if t.phase != trapsSet || action == "" {
		return "", false
	}
	t.phase, t.before = signalTrapRunning, before
	return action, true
}

// startLineTrap starts a run of the shell's trap for sig, a signal that
// did not end the shell, after a shell status of before. ok is false, and
// nothing starts, when the shell has no trap for it or its end has started.
func (t *shellTraps) startLineTrap(sig syscall.Signal, before int) (action string, ok bool) {
	cond, ok := signalCondition(sig)
	if !ok {
		return "", false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	action = t.shell[cond]
	if t.phase != trapsSet || action == "" {
		return "", false
	}
	t.phase, t.before = lineTrapRunning, before
	return action, true
}

// startExitTrap starts the one run of the shell's EXIT trap, after a
// shell status of before; ok is false when there is none to run. The trap
// is no longer listed from here.
func (t *shellTraps) startExitTrap(before int) (action string, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.phase != trapsSet && t.phase != signalTrapDone {
		return "", false
	}
	action = t.shell["EXIT"]
	delete(t.shell, "EXIT")
	if action == "" {
		t.phase = trapsDone
		return "", false
	}
	t.phase, t.before = exitTrapRunning, before
	return action, true
}

// endTrap ends the run of the trap that runs.
func (t *shellTraps) endTrap() {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch t.phase {
	case lineTrapRunning:
		t.phase = trapsSet
	case signalTrapRunning:
		t.phase = signalTrapDone
	case exitTrapRunning:
		t.phase = trapsDone
	}
}

// replaced records that exec replaced the shell with a command: its
// traps go, and none runs at its end.
func (t *shellTraps) replaced() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.shell = nil
	t.phase = trapsDone
}

// bareExit is the call for an exit with no status: in a running trap of
// the shell's, an exit with the shell's status from before the trap;
// anywhere else the call as it is ($?).
func (t *shellTraps) bareExit(args []string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(args) != 1 || (t.phase != lineTrapRunning && t.phase != signalTrapRunning && t.phase != exitTrapRunning) {
		return args
	}
	return []string{"exit", strconv.Itoa(t.before)}
}

// handleTrapCall takes the traps hash keeps (EXIT, the fatal signals) out
// of a `trap` call, for the shell or the subshell that made it, lists the
// traps for a listing call, and leaves the rest (ERR, errors) to the
// interpreter's builtin.
func (e *Executor) handleTrapCall(ctx context.Context, args []string) []string {
	rest := args[1:]
	listing := false
	if len(rest) > 0 && rest[0] == "-p" {
		listing, rest = true, rest[1:]
	}
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}
	if listing || len(rest) == 0 {
		e.listTraps(ctx, rest)
		return []string{":"}
	}
	action, specs := "-", rest
	if len(rest) > 1 {
		action, specs = rest[0], rest[1:]
	}
	if len(action) > 1 && (action[0] == '-' || action[0] == '+') {
		return args // a flag: the builtin reports it
	}
	caller := e.callerRunner(ctx)
	var kept []string
	for _, spec := range specs {
		if cond, ok := trapCondition(spec); ok {
			e.traps.set(caller, e.runner, cond, action)
			continue
		}
		if strings.EqualFold(spec, "ERR") {
			spec = "ERR" // the builtin takes only the upper case
		}
		kept = append(kept, spec)
	}
	if len(kept) == 0 {
		return []string{":"}
	}
	return append([]string{"trap", "--", action}, kept...)
}

// handleExecCall makes an exec of a command by the shell itself the call
// of execCommand, which replaces the shell only once the command started
// (execReplacing): the interpreter's exec ends the shell whether the
// command ran or not. An exec with no command (exec >file) and one in a
// subshell, which ends only the subshell, go to the interpreter's.
func (e *Executor) handleExecCall(ctx context.Context, args []string) []string {
	if len(args) < 2 || e.callerRunner(ctx) != e.runner {
		return args
	}
	if args[1] == "--" {
		args = args[1:]
	}
	return append([]string{execCommand}, args[1:]...)
}

// execCommand is a command name only hash's exec handler answers: the
// shell's exec of the command its arguments name (execReplacing). The NUL
// keeps it out of reach of a command a user can type.
const execCommand = "\x00hash-exec"

// execReplacing runs args, the command of an exec that replaces the shell.
// Once the command started the shell's traps go (bash's EXIT trap never
// runs after an exec), and once it ended the line ends the shell with its
// status. One that is not there (127) or cannot start (126) leaves the
// interactive shell running, its traps kept, as bash's; it ends a script
// with that status, one that could not start with no EXIT trap, as bash's.
func (e *Executor) execReplacing(ctx context.Context, args []string) error {
	lr := lineRunFrom(ctx)
	if len(args) == 0 || lr == nil {
		return nil
	}
	started := false
	err := e.runExternal(withCommandStarted(ctx, func() {
		started = true
		e.traps.replaced()
	}), args)
	var status interp.ExitStatus
	if err != nil && !errors.As(err, &status) {
		return err // hash's own error
	}
	switch {
	case started:
	case e.terminal == terminalInteractive:
		return err
	case status == 126:
		e.traps.replaced()
	}
	lr.requestExit(int(status))
	return err
}

// listTraps prints the traps as commands that set them again, EXIT, the
// signals, then ERR, or only the ones named in only (trap -p INT).
func (e *Executor) listTraps(ctx context.Context, only []string) {
	stdout := interp.HandlerCtx(ctx).Stdout
	traps, errAction := e.listedTraps(ctx)
	if action, ok := traps["EXIT"]; ok && trapNamed(only, "EXIT") {
		fmt.Fprintf(stdout, "trap -- %s EXIT\n", shellQuote(action))
	}
	for _, ts := range trapSignals {
		if action, ok := traps[ts.name]; ok && trapNamed(only, ts.name) {
			fmt.Fprintf(stdout, "trap -- %s SIG%s\n", shellQuote(action), ts.name)
		}
	}
	if errAction != "" && trapNamed(only, "ERR") {
		fmt.Fprintf(stdout, "trap -- %s ERR\n", shellQuote(errAction))
	}
}

// listedTraps are the traps a trap call lists: the caller's own. A
// subshell (a command substitution, as in `saved=$(trap)`) lists the
// shell's until it runs a trap command of its own, as in bash. A
// background job lists only its own: the shell's may change under it.
func (e *Executor) listedTraps(ctx context.Context) (traps trapSet, errAction string) {
	caller := e.callerRunner(ctx)
	traps, own := e.traps.of(caller, e.runner)
	errAction = runnerErrTrap(caller)
	if caller == e.runner || inBackgroundJob(ctx) {
		return traps, errAction
	}
	if !own {
		traps, _ = e.traps.of(e.runner, e.runner)
	}
	if errAction == "" {
		errAction = runnerErrTrap(e.runner)
	}
	return traps, errAction
}

// trapNamed reports whether a listing of only shows cond: all do when only
// names none.
func trapNamed(only []string, cond string) bool {
	if len(only) == 0 {
		return true
	}
	for _, spec := range only {
		if c, ok := trapCondition(spec); (ok && c == cond) || (cond == "ERR" && strings.EqualFold(spec, "ERR")) {
			return true
		}
	}
	return false
}

// SetLastStatus sets $? to code without running anything: no ERR trap or
// errexit fires, and no line starts (no progress, status report or
// capture). The shell uses it for a line the interpreter did not run.
func (e *Executor) SetLastStatus(code int) {
	e.runnerMu.Lock()
	defer e.runnerMu.Unlock()
	if e.runner == nil {
		if err := e.initRunner(); err != nil {
			return
		}
	}
	setRunnerLastStatus(e.runner, code)
}

// lastStatus is the shell's $?.
func (e *Executor) lastStatus() int {
	e.runnerMu.Lock()
	defer e.runnerMu.Unlock()
	if e.runner == nil {
		return 0
	}
	status, _ := runnerLastStatus(e.runner)
	return status
}

// IgnoresSignal reports whether the shell's trap for sig is an empty one
// (trap "" TERM): the signal must not end the shell.
func (e *Executor) IgnoresSignal(sig syscall.Signal) bool {
	return e.traps.ignores(sig)
}

// RunSignalTrap runs the shell's trap for sig, once, when sig ends the
// shell, before the EXIT trap. ran is false when the shell has no trap for
// it. An exit in the trap ends the shell as any exit does: exited reports
// one, with its status (a bare exit's is $? from before the trap).
func (e *Executor) RunSignalTrap(ctx context.Context, sig syscall.Signal, stdout, stderr io.Writer) (status int, exited, ran bool) {
	action, ok := e.traps.startSignalTrap(sig, e.lastStatus())
	if !ok {
		return 0, false, false
	}
	defer e.traps.endTrap()
	status, exited = e.runTrap(ctx, action, stdout, stderr)
	return status, exited, true
}

// runOwedTrap runs the shell's trap for sig, a signal that did not end
// the shell (owedTraps), with $? status, which the shell keeps after it.
// bash runs it once the command running returned (hash cannot stop a line
// in the middle). An exit in the trap ends the shell: exited reports one,
// with its status.
func (e *Executor) runOwedTrap(ctx context.Context, sig syscall.Signal, status int, stdout, stderr io.Writer) (exitStatus int, exited bool) {
	action, ok := e.traps.startLineTrap(sig, status)
	if !ok {
		return 0, false
	}
	defer e.traps.endTrap()
	if code, exited := e.runTrap(ctx, action, stdout, stderr); exited {
		return code, true
	}
	e.SetLastStatus(status)
	return 0, false
}

// owedTraps is the traps of the signals that came without ending the
// shell, in the order they came, each once: SIGINT a foreground command
// handled, and the signals no shell ends on (SignalArrived). A signal that
// comes during a line is owed to it: its trap runs once the line's own
// work ended (Executor.executeLine), with the line's $?, as the line still
// counts as in flight, so that one that comes during that trap is owed to
// the line too. One that comes with no line
// running (a script waiting for its next line) is owed to the next line:
// its trap runs before it, with the last line's $?, as bash 5.2 runs a
// trap once the read of its next line returned.
type owedTraps struct {
	mu    sync.Mutex
	lines int              // lines in flight: a trap's own line runs inside one
	line  []syscall.Signal // owed to the lines in flight, run as the outermost ends
	idle  []syscall.Signal // came with no line in flight: run before the next one
	// watched: the signals the shell is told of as they come
	// (WatchSignals); a command's death of one may have it on its way.
	watched []syscall.Signal
	came    chan struct{} // closed as a signal is owed, for await; nil until one waits
}

func (o *owedTraps) owe(sig syscall.Signal) {
	o.mu.Lock()
	defer o.mu.Unlock()
	owed := &o.idle
	if o.lines > 0 {
		owed = &o.line
	}
	if !slices.Contains(*owed, sig) {
		*owed = append(*owed, sig)
	}
	if o.came != nil {
		close(o.came)
		o.came = nil
	}
}

// watch records that the shell is told of each of sigs as it comes.
func (o *owedTraps) watch(sigs ...syscall.Signal) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.watched = append(o.watched, sigs...)
}

// watches reports whether the shell is told of sig as it comes.
func (o *owedTraps) watches(sig syscall.Signal) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Contains(o.watched, sig)
}

// await waits until sig is owed, for at most within.
func (o *owedTraps) await(sig syscall.Signal, within time.Duration) {
	timer := time.NewTimer(within)
	defer timer.Stop()
	for {
		o.mu.Lock()
		if slices.Contains(o.line, sig) || slices.Contains(o.idle, sig) {
			o.mu.Unlock()
			return
		}
		if o.came == nil {
			o.came = make(chan struct{})
		}
		came := o.came
		o.mu.Unlock()
		select {
		case <-came:
		case <-timer.C:
			return
		}
	}
}

// lineStarts records a line in flight and returns the signals that came
// with none, whose traps run before it; none for a line inside a line.
func (o *owedTraps) lineStarts() (idle []syscall.Signal) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lines++
	if o.lines > 1 {
		return nil
	}
	idle, o.idle = o.idle, nil
	return idle
}

// takeLineOwed returns the signals that came during the outermost line in
// flight so far, whose traps run now, as it still counts as in flight: a
// signal that comes while one of them runs is owed to it too, for the
// next take. None for a line inside a line (a trap's own), whose outer
// line runs them.
func (o *owedTraps) takeLineOwed() (owed []syscall.Signal) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.lines > 1 {
		return nil
	}
	owed, o.line = o.line, nil
	return owed
}

// lineEnds records the end of a line in flight. A signal that came during
// the outermost line after its last take is owed to the next line.
func (o *owedTraps) lineEnds() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lines--
	if o.lines > 0 {
		return
	}
	for _, sig := range o.line {
		if !slices.Contains(o.idle, sig) {
			o.idle = append(o.idle, sig)
		}
	}
	o.line = nil
}

// runsLineTrap reports whether the shell runs its trap for sig after a
// line: the interactive shell runs no INT trap, as bash 5.2's runs none
// when a command handled the SIGINT; a script does.
func (e *Executor) runsLineTrap(sig syscall.Signal) bool {
	return sig != syscall.SIGINT || e.terminal != terminalInteractive
}

// WatchSignals tells the executor that its caller tells it of each of sigs
// as it comes (SignalArrived), from now on: a foreground command that dies
// of one the shell traps then waits a moment for the shell's own
// (lateSignalOf).
func (e *Executor) WatchSignals(sigs ...syscall.Signal) {
	e.owed.watch(sigs...)
}

// lateSignalOf is the signal ps died of, and whether the shell's own copy
// of it may still be on its way with a trap to run for it: Ctrl+\ sends
// SIGQUIT to the shell and its foreground commands at once, and Go hands
// the shell its copy on another goroutine, so the command's end can be
// seen first; its trap must run for the line the command ended. That is a
// signal the shell is told of (WatchSignals) and traps. (SIGINT has its
// own wait: foreground.)
func (e *Executor) lateSignalOf(ps *os.ProcessState) (syscall.Signal, bool) {
	if ps == nil {
		return 0, false
	}
	ws, ok := ps.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return 0, false
	}
	sig := ws.Signal()
	return sig, e.owed.watches(sig) && e.traps.traps(sig)
}

// SignalArrived tells the executor that the shell got sig, which does not
// end it: SIGQUIT (Ctrl+\), as bash 5.2 ignores it, and SIGTERM in an
// interactive shell. The foreground command dies of it, and the shell's
// trap for it, when there is one, runs once the line ended.
func (e *Executor) SignalArrived(sig syscall.Signal) {
	e.owed.owe(sig)
}

// RunExitTrap runs the shell's EXIT trap, once, with $? the status of the
// last line. Hash calls it when the shell exits. An exit in the trap sets
// the shell's exit status, as in bash: exited reports one, with its status
// (a bare exit's is the status from before the trap).
func (e *Executor) RunExitTrap(ctx context.Context, stdout, stderr io.Writer) (status int, exited bool) {
	if e == nil {
		return 0, false
	}
	action, ok := e.traps.startExitTrap(e.lastStatus())
	if !ok {
		return 0, false
	}
	defer e.traps.endTrap()
	return e.runTrap(ctx, action, stdout, stderr)
}

func (e *Executor) runTrap(ctx context.Context, action string, stdout, stderr io.Writer) (status int, exited bool) {
	res, err := e.Execute(ctx, action, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "hash: %v\n", err)
		return 0, false
	}
	return res.ExitCode, res.End == LineExited
}
