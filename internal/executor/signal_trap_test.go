package executor

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// trap takes INT, TERM, HUP and QUIT as bash does (any case, SIG or not, or
// the number) and lists them after EXIT as bash names them; `trap -` and
// `trap SIG` reset one. The interpreter rejected them, which aborted
// `set -e` scripts at `trap cleanup EXIT INT TERM`.
func TestExecutor_TrapKeepsSignalTraps(t *testing.T) {
	e := New()
	got := execLine(t, e, `set -e; trap 'a' int; trap 'b' SIGTERM; trap 'c' 1; trap 'q' quit; trap 'e' exit; trap; echo ok`)
	want := "trap -- 'e' EXIT\ntrap -- 'c' SIGHUP\ntrap -- 'a' SIGINT\ntrap -- 'q' SIGQUIT\ntrap -- 'b' SIGTERM\nok\n"
	if got != want {
		t.Fatalf("listing = %q, want %q", got, want)
	}
	if got := execLine(t, e, `trap -p int 15`); got != "trap -- 'a' SIGINT\ntrap -- 'b' SIGTERM\n" {
		t.Errorf("trap -p int 15 = %q", got)
	}
	if got := execLine(t, e, `trap - INT; trap sigterm; trap -- - 1 QUIT; trap -p`); got != "trap -- 'e' EXIT\n" {
		t.Errorf("after resets: %q, want only EXIT", got)
	}
	if got := execLine(t, e, `trap 'echo e' err; false; trap - ERR; true`); got != "e\n" {
		t.Errorf("trap ... err: %q, want e", got)
	}
}

// The shell's trap for a signal runs once, when that signal ends the shell,
// before the EXIT trap; an exit in it is the shell's, and a bare exit keeps
// the status from before the trap.
func TestExecutor_RunSignalTrap(t *testing.T) {
	e := New()
	if _, _, ran := e.RunSignalTrap(context.Background(), syscall.SIGTERM, io.Discard, io.Discard); ran {
		t.Fatal("RunSignalTrap ran with no trap")
	}
	execLine(t, e, `trap 'echo term $?' TERM; trap 'echo exit $?' EXIT`)
	e.SetLastStatus(143)
	var out bytes.Buffer
	if _, exited, ran := e.RunSignalTrap(context.Background(), syscall.SIGTERM, &out, io.Discard); !ran || exited {
		t.Fatalf("RunSignalTrap: ran %v, exited %v; want ran, no exit", ran, exited)
	}
	if _, _, ran := e.RunSignalTrap(context.Background(), syscall.SIGTERM, &out, io.Discard); ran {
		t.Error("the signal trap ran twice")
	}
	e.RunExitTrap(context.Background(), &out, io.Discard)
	e.RunExitTrap(context.Background(), &out, io.Discard)
	if got := out.String(); got != "term 143\nexit 0\n" {
		t.Errorf("traps printed %q, want term 143 then exit 0, once each", got)
	}

	e = New()
	execLine(t, e, `trap 'false; exit' INT`)
	e.SetLastStatus(130)
	if status, exited, _ := e.RunSignalTrap(context.Background(), syscall.SIGINT, io.Discard, io.Discard); !exited || status != 130 {
		t.Errorf("bare exit in the INT trap: %d, %v; want 130, true", status, exited)
	}
}

// An empty trap ignores its signal.
func TestExecutor_EmptySignalTrapIgnores(t *testing.T) {
	e := New()
	execLine(t, e, `trap '' TERM; trap 'x' INT`)
	if !e.IgnoresSignal(syscall.SIGTERM) || e.IgnoresSignal(syscall.SIGINT) || e.IgnoresSignal(syscall.SIGHUP) {
		t.Errorf("IgnoresSignal TERM %v INT %v HUP %v, want only TERM", e.IgnoresSignal(syscall.SIGTERM), e.IgnoresSignal(syscall.SIGINT), e.IgnoresSignal(syscall.SIGHUP))
	}
}

// exec with a command replaces the shell, so its EXIT trap never runs, as
// in bash; an exec that cannot run its command, or one in a subshell,
// leaves the shell's traps alone.
func TestExecutor_ExecDropsTheShellsTraps(t *testing.T) {
	e := New()
	if got := execLine(t, e, `trap 'echo bye' EXIT; trap 'echo term' TERM; exec echo hi`); got != "hi\n" {
		t.Fatalf("exec echo hi printed %q", got)
	}
	var out bytes.Buffer
	e.RunExitTrap(context.Background(), &out, io.Discard)
	if out.Len() != 0 {
		t.Errorf("the EXIT trap ran after exec: %q", out.String())
	}
	if _, _, ran := e.RunSignalTrap(context.Background(), syscall.SIGTERM, io.Discard, io.Discard); ran {
		t.Error("the TERM trap ran after exec")
	}

	for _, line := range []string{`exec hash_no_such_cmd_xyz`, `(exec echo sub)`, `exec >/dev/null`} {
		e := New()
		execLine(t, e, `trap 'echo bye' EXIT`)
		_, _ = e.Execute(context.Background(), line, io.Discard, io.Discard)
		out.Reset()
		e.RunExitTrap(context.Background(), &out, io.Discard)
		if out.String() != "bye\n" {
			t.Errorf("after %q the EXIT trap printed %q, want bye", line, out.String())
		}
	}
}

// withHandledInterrupt is e with a builtin, handled_int, that stands for a
// foreground command that handled the shell's SIGINT: the line owes the
// INT trap a run.
func withHandledInterrupt(e *Executor) *Executor {
	e.RegisterBuiltin("handled_int", func(context.Context, []string, io.Writer, io.Writer) error {
		e.owed.owe(syscall.SIGINT)
		return nil
	})
	return e
}

// After a line whose command handled the shell's SIGINT (it owes the INT
// trap a run), the INT trap
// runs with $? the line's status, which stays after it; an exit in it
// ends the shell, a bare one with the line's status. The trap stays set,
// and the shell's end still runs its traps.
func TestExecutor_InterruptTrapRunsAfterTheLine(t *testing.T) {
	e := withHandledInterrupt(New())
	execLine(t, e, `trap 'echo int $?' INT; trap 'echo bye' EXIT`)
	if got := execLine(t, e, `handled_int; (exit 3)`); got != "int 3\n" {
		t.Errorf("output %q, want int 3", got)
	}
	if got := execLine(t, e, `echo $?`); got != "3\n" {
		t.Errorf("$? after the trap = %q, want 3", got)
	}
	if got := execLine(t, e, `(exit 4)`); got != "" {
		t.Errorf("the trap ran again with no SIGINT: %q", got)
	}

	for _, tc := range []struct{ trap, want string }{{"exit 7", "7"}, {"exit", "3"}} {
		e := withHandledInterrupt(New())
		execLine(t, e, `trap '`+tc.trap+`' INT`)
		res, err := e.Execute(context.Background(), `handled_int; (exit 3)`, io.Discard, io.Discard)
		if err != nil || res.End != LineExited || strconv.Itoa(res.ExitCode) != tc.want {
			t.Errorf("trap %q: result %+v, %v; want exited with %s", tc.trap, res, err, tc.want)
		}
	}

	var out bytes.Buffer
	if status, exited := e.RunExitTrap(context.Background(), &out, io.Discard); exited || status != 0 || out.String() != "bye\n" {
		t.Errorf("EXIT trap after the INT trap: %q, %d, %v", out.String(), status, exited)
	}
}

// A signal that ends no shell (Ctrl+\, SignalArrived) runs its trap once
// the line it came during ended, with $? the line's status; one that came
// between lines runs its trap before the next line, with $? the last
// line's, as bash 5.2 runs a USR1 trap that came while it read its next
// line. It used to wait for the next line to end, and saw that line's $?.
func TestExecute_SignalTrapRunsForTheLineItCameIn(t *testing.T) {
	t.Parallel()
	e := New()
	var out bytes.Buffer
	run := func(line string) {
		t.Helper()
		if _, err := e.Execute(context.Background(), line, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	run(`trap 'echo "quit $?"' QUIT; false`)
	e.SignalArrived(syscall.SIGQUIT) // between lines
	run(`echo next`)
	if want := "quit 1\nnext\n"; out.String() != want {
		t.Errorf("between lines: %q, want %q", out.String(), want)
	}
	out.Reset()
	go func() {
		time.Sleep(100 * time.Millisecond)
		e.SignalArrived(syscall.SIGQUIT) // during the line
	}()
	run(`sleep 0.3; echo during; (exit 3)`)
	if want := "during\nquit 3\n"; out.String() != want {
		t.Errorf("during a line: %q, want %q", out.String(), want)
	}
}

// Ctrl+\ sends SIGQUIT to the script and its foreground command at once,
// but Go hands the shell its copy on another goroutine: the command's end
// can be seen first. Its trap still runs for the line the command ended,
// with that line's $?: a command that died of a signal the shell watches
// and traps waits a moment for the shell's own (lateSIGINT). It used to
// run after the next line, with that line's $?.
func TestExecute_SignalTrapWaitsForTheShellsSignalAfterTheCommandDied(t *testing.T) {
	e := New()
	e.WatchSignals(syscall.SIGQUIT)
	var out bytes.Buffer
	run := func(line string) {
		t.Helper()
		if _, err := e.Execute(context.Background(), line, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	run(`trap 'echo "quit $?"' QUIT`)
	start := time.Now()
	go func() {
		// The shell's copy, a moment after the command died of its own.
		time.Sleep(60 * time.Millisecond)
		e.SignalArrived(syscall.SIGQUIT)
	}()
	run(`sh -c 'kill -QUIT $$'`)
	took := time.Since(start)
	run(`echo next $?`)
	if want := "quit 131\nnext 131\n"; out.String() != want {
		t.Errorf("the shell's signal after the command's death: %q, want %q (the line took %v)", out.String(), want, took)
	}
	// Only a death of a signal the shell watches and traps waits: with no
	// trap, or no one telling the shell of the signal, nothing comes.
	quit := diedOfQuit(t)
	if _, wait := e.lateSignalOf(quit); !wait {
		t.Error("a QUIT death with a QUIT trap waits for nothing")
	}
	run(`trap - QUIT`)
	if _, wait := e.lateSignalOf(quit); wait {
		t.Error("a QUIT death with no trap waits")
	}
	run(`trap 'echo quit' QUIT`)
	if _, wait := New().lateSignalOf(quit); wait {
		t.Error("an executor no one tells of QUIT waits for it")
	}
}

// diedOfQuit is the state of a process that died of SIGQUIT.
func diedOfQuit(t *testing.T) *os.ProcessState {
	t.Helper()
	cmd := exec.Command("sh", "-c", "kill -QUIT $$")
	_ = cmd.Run()
	if !diedOf(cmd.ProcessState, syscall.SIGQUIT) {
		t.Fatalf("sh ended %v, want killed by SIGQUIT", cmd.ProcessState)
	}
	return cmd.ProcessState
}

// owedTraps owes a signal to the line in flight, or, with none, to the
// next line; a line inside a line (a trap's) leaves them to the outer one,
// which takes them as long as it is in flight; one that came after its
// last take is the next line's; each signal is owed once.
func TestOwedTraps_Transitions(t *testing.T) {
	var o owedTraps
	o.owe(syscall.SIGQUIT) // idle
	o.owe(syscall.SIGQUIT)
	if got := o.lineStarts(); len(got) != 1 || got[0] != syscall.SIGQUIT {
		t.Fatalf("the line starts owing %v, want [quit]", got)
	}
	o.owe(syscall.SIGTERM) // during the line
	if got := o.lineStarts(); got != nil {
		t.Fatalf("a line inside a line ran %v before it", got)
	}
	o.owe(syscall.SIGINT)
	if got := o.takeLineOwed(); got != nil {
		t.Fatalf("the inner line took %v", got)
	}
	o.lineEnds()
	if got := o.takeLineOwed(); len(got) != 2 || got[0] != syscall.SIGTERM || got[1] != syscall.SIGINT {
		t.Fatalf("the line owes %v, want [term int]", got)
	}
	o.owe(syscall.SIGUSR1) // during an owed trap: the line still takes it
	if got := o.takeLineOwed(); len(got) != 1 || got[0] != syscall.SIGUSR1 {
		t.Fatalf("the line owes %v after its first take, want [usr1]", got)
	}
	if got := o.takeLineOwed(); got != nil {
		t.Fatalf("the line owes %v again", got)
	}
	o.lineStarts() // an owed trap's own line
	o.owe(syscall.SIGUSR2)
	o.lineEnds()
	o.lineEnds() // after the outer line's last take
	if got := o.lineStarts(); len(got) != 1 || got[0] != syscall.SIGUSR2 {
		t.Fatalf("the next line starts owing %v, want [usr2]", got)
	}
	o.lineEnds()
}

// A signal that comes while the trap of another runs after a line still
// gets its trap, once that one ended. The line counted as ended before its
// owed traps ran, so the trap's own line took the signal as one owed to it
// and could not start its trap while the first one ran: the signal was
// dropped. (One that came after the line, just before the trap's own line
// started, was taken by that line and dropped the same way.)
func TestExecute_SignalDuringAnOwedTrapRunsItsTrap(t *testing.T) {
	e := New()
	e.RegisterBuiltin("arrive", func(_ context.Context, args []string, _, _ io.Writer) error {
		sig, _ := signalNamed(args[0])
		e.SignalArrived(sig)
		return nil
	})
	var out bytes.Buffer
	for _, line := range []string{
		`trap 'echo "quit $?"; arrive TERM' QUIT; trap 'echo "term $?"' TERM`,
		`arrive QUIT; (exit 3)`,
		`echo next`,
	} {
		if _, err := e.Execute(context.Background(), line, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	if want := "quit 3\nterm 3\nnext\n"; out.String() != want {
		t.Errorf("out %q, want %q", out.String(), want)
	}
}
