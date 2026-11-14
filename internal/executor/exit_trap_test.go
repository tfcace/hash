package executor

import (
	"bytes"
	"context"
	"io"
	"runtime"
	"testing"
	"time"
)

// An interactive shell runs its EXIT trap when it exits, not at the end of
// every line: `trap 'kill $SSH_AGENT_PID' EXIT` in an rc file must not fire
// after the first command.
func TestExecutor_ExitTrapWaitsForTheShellToExit(t *testing.T) {
	e := New()
	line := func(cmd string) string {
		var out bytes.Buffer
		if _, err := e.Execute(context.Background(), cmd, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if got := line(`trap 'echo "TRAP rc=$?"' EXIT; echo set`); got != "set\n" {
		t.Fatalf("line setting the trap printed %q, want only set", got)
	}
	if got := line(`trap`); got != `trap -- 'echo "TRAP rc=$?"' EXIT`+"\n" {
		t.Fatalf("trap listing = %q", got)
	}
	if got := line(`echo next; false`); got != "next\n" {
		t.Fatalf("a later line printed %q, want only next", got)
	}
	var out bytes.Buffer
	e.RunExitTrap(context.Background(), &out, io.Discard)
	if out.String() != "TRAP rc=1\n" {
		t.Fatalf("exit trap printed %q, want it once with the last status", out.String())
	}
	out.Reset()
	e.RunExitTrap(context.Background(), &out, io.Discard)
	if out.Len() != 0 {
		t.Fatalf("exit trap ran twice: %q", out.String())
	}
}

func TestExecutor_ExitTrapCanBeReset(t *testing.T) {
	e := New()
	if _, err := e.Execute(context.Background(), `trap 'echo TRAP' EXIT; trap - EXIT`, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	e.RunExitTrap(context.Background(), &out, io.Discard)
	if out.Len() != 0 {
		t.Fatalf("a reset exit trap ran: %q", out.String())
	}
}

// A line that runs `exit` (anywhere: `true && exit 4`) tells the shell, so
// it can exit; the status is the exit's.
func TestExecute_ReportsALineThatExited(t *testing.T) {
	e := New()
	res, err := e.Execute(context.Background(), `true && exit 4; echo no`, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if res.End != LineExited || res.ExitCode != 4 {
		t.Fatalf("End = %v, ExitCode = %d; want LineExited, 4", res.End, res.ExitCode)
	}
	if res, _ := e.Execute(context.Background(), `( exit 2 )`, io.Discard, io.Discard); res.End == LineExited {
		t.Fatal("a subshell's exit reported the shell as exited")
	}
}

func execLine(t *testing.T, e *Executor, cmd string) string {
	t.Helper()
	var out bytes.Buffer
	if _, err := e.Execute(context.Background(), cmd, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// An exit in the EXIT trap sets the shell's status, as in bash
// (`bash -c 'trap "exit 9" EXIT; exit 3'` exits 9); a trap that does not
// exit leaves it alone.
func TestExecutor_ExitTrapExitSetsTheStatus(t *testing.T) {
	e := New()
	execLine(t, e, `trap 'exit 9' EXIT`)
	if status, exited := e.RunExitTrap(context.Background(), io.Discard, io.Discard); !exited || status != 9 {
		t.Fatalf("RunExitTrap = %d, %v; want 9, true", status, exited)
	}
	e = New()
	execLine(t, e, `trap false EXIT`)
	if _, exited := e.RunExitTrap(context.Background(), io.Discard, io.Discard); exited {
		t.Fatal("a trap that did not exit reported an exit")
	}
}

// The listing quotes the action for the shell, so it can be read back.
func TestExecutor_ExitTrapListingIsShellQuoted(t *testing.T) {
	e := New()
	execLine(t, e, `trap 'echo "it'\''s $x"' EXIT`)
	want := `trap -- 'echo "it'\''s $x"' EXIT` + "\n"
	for _, cmd := range []string{`trap`, `trap -p`, `trap -p EXIT`, `trap -p 0`} {
		if got := execLine(t, e, cmd); got != want {
			t.Errorf("%s = %q, want %q", cmd, got, want)
		}
	}
}

// `s=$(trap)` saves the traps to restore them later, as in bash: a command
// substitution lists the shell's traps.
func TestExecutor_ExitTrapListedInCommandSubstitution(t *testing.T) {
	e := New()
	execLine(t, e, `trap 'echo bye' EXIT; s=$(trap); trap - EXIT; eval "$s"`)
	var out bytes.Buffer
	e.RunExitTrap(context.Background(), &out, io.Discard)
	if out.String() != "bye\n" {
		t.Fatalf("restored trap printed %q, want bye", out.String())
	}
	if got := execLine(t, e, `trap 'echo a' EXIT; echo "$(trap -p EXIT)"; (trap 'echo sub' EXIT; trap; trap - EXIT)`); got != "trap -- 'echo a' EXIT\ntrap -- 'echo sub' EXIT\n" {
		t.Fatalf("subshell listings = %q", got)
	}
}

// An empty action sets an empty trap (listed, runs nothing), which is not the
// same as no trap.
func TestExecutor_EmptyExitTrapIsATrap(t *testing.T) {
	e := New()
	if got := execLine(t, e, `trap '' EXIT; trap`); got != "trap -- '' EXIT\n" {
		t.Fatalf("listing = %q, want the empty trap", got)
	}
	if got := execLine(t, e, `trap - EXIT; trap`); got != "" {
		t.Fatalf("after reset, listing = %q, want nothing", got)
	}
}

// SetLastStatus sets $? without running a command: the ERR trap does not
// fire, before or after the runner's first line.
func TestExecutor_SetLastStatusRunsNothing(t *testing.T) {
	e := New()
	e.SetLastStatus(4) // before any line
	if got := execLine(t, e, `echo $?`); got != "4\n" {
		t.Fatalf("$? = %q, want 4", got)
	}
	execLine(t, e, `trap 'echo ERR-FIRED' ERR`)
	e.SetLastStatus(3)
	if got := execLine(t, e, `echo $?`); got != "3\n" {
		t.Fatalf("$? = %q, want 3", got)
	}
}

// SetLastStatus replaces the whole last status, not just its code: a bare
// exit after it exits with the status it set. Keeping the failed command's
// error made `exit` exit with 1 (code 0 with an error) or the older status.
func TestExecutor_SetLastStatusThenBareExit(t *testing.T) {
	for _, failing := range []string{"sh -c 'exit 3'", "false", "(exit 4)"} {
		for _, code := range []int{0, 5} {
			e := New()
			ctx := context.Background()
			_, _ = e.Execute(ctx, failing, io.Discard, io.Discard)
			e.SetLastStatus(code)
			res, err := e.Execute(ctx, "exit", io.Discard, io.Discard)
			if err != nil {
				t.Fatalf("%s; SetLastStatus(%d); exit: %v", failing, code, err)
			}
			if res.End != LineExited || res.ExitCode != code {
				t.Errorf("%s; SetLastStatus(%d); exit: status %d (end %v), want %d", failing, code, res.ExitCode, res.End, code)
			}
		}
	}
}

// A subshell that resets its EXIT trap has none: its listing shows nothing,
// not the shell's trap, as in bash (a subshell lists the shell's traps only
// until it sets or resets its own).
func TestExecutor_SubshellResetExitTrapIsListedAsNone(t *testing.T) {
	e := New()
	got := execLine(t, e, `trap 'echo p' EXIT; x=$(trap - EXIT; trap); echo "[$x]"; (trap 'echo s' EXIT; trap - EXIT; trap; echo end); echo "[$(trap '' EXIT; trap)]"`)
	if want := "[]\nend\n[trap -- '' EXIT]\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

// A zsh-only program run in a subshell (runNested) acts on the subshell's
// EXIT trap as any other command does: a reset in it stays reset, a trap it
// sets is the subshell's and does not fire when the program ends.
func TestExecutor_NestedZshRunSetsTheSubshellsExitTrap(t *testing.T) {
	e := New()
	if err := e.SetDialect("zsh"); err != nil {
		t.Fatal(err)
	}
	execLine(t, e, `x=ab`)
	if got := execLine(t, e, `(trap 'echo sub' EXIT; eval 'echo ${(U)x} >/dev/null; trap - EXIT'; trap; echo end)`); got != "end\n" {
		t.Errorf("reset in a nested run: stdout = %q, want only end", got)
	}
	if got := execLine(t, e, `(eval 'echo ${(U)x} >/dev/null; trap "echo in" EXIT'; echo after; trap)`); got != "after\ntrap -- 'echo in' EXIT\n" {
		t.Errorf("set in a nested run: stdout = %q, want after and the listing", got)
	}
}

// A subshell's trap is kept only while its runner lives: a loop of
// subshells that set traps does not grow the shell.
func TestShellTraps_SubshellTrapGoesWithItsRunner(t *testing.T) {
	e := New()
	execLine(t, e, `for i in 1 2 3 4 5 6 7 8; do (trap 'echo s' EXIT; trap - EXIT); x=$(trap 'echo c' EXIT); done`)
	deadline := time.Now().Add(5 * time.Second)
	for {
		runtime.GC()
		e.traps.mu.Lock()
		n := len(e.traps.subshells)
		e.traps.mu.Unlock()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d subshell traps kept after their subshells ended", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// `builtin trap` and `command trap` set the same EXIT trap as trap: it used
// to reach the interpreter and fire after every line.
func TestExecutor_BuiltinTrapSetsTheShellsExitTrap(t *testing.T) {
	for _, prefix := range []string{"builtin", "command"} {
		e := New()
		if got := execLine(t, e, prefix+` trap 'echo FIRED' EXIT; echo one`); got != "one\n" {
			t.Errorf("%s trap: line printed %q, want only one", prefix, got)
		}
		if got := execLine(t, e, prefix+` trap -p EXIT`); got != "trap -- 'echo FIRED' EXIT\n" {
			t.Errorf("%s trap -p EXIT = %q", prefix, got)
		}
		var out bytes.Buffer
		e.RunExitTrap(context.Background(), &out, io.Discard)
		if out.String() != "FIRED\n" {
			t.Errorf("%s trap: exit trap printed %q, want FIRED", prefix, out.String())
		}
	}
}

// A bare exit in the EXIT trap exits with the status the shell had before
// the trap, as POSIX says and bash does (`trap "echo x; exit" EXIT; (exit
// 7)` exits 7), not the status of the trap's last command.
func TestExecutor_BareExitInTheExitTrapKeepsTheShellsStatus(t *testing.T) {
	for _, trap := range []string{`echo x; exit`, `false; exit`, `f() { true; exit; }; f`} {
		e := New()
		execLine(t, e, `trap '`+trap+`' EXIT`)
		execLine(t, e, `(exit 7)`)
		if status, exited := e.RunExitTrap(context.Background(), io.Discard, io.Discard); !exited || status != 7 {
			t.Errorf("trap %q after status 7: RunExitTrap = %d, %v; want 7, true", trap, status, exited)
		}
		// Outside the trap a bare exit is $? again.
		if res, _ := e.Execute(context.Background(), `false; exit`, io.Discard, io.Discard); res.ExitCode != 1 {
			t.Errorf("bare exit after the trap: status %d, want 1", res.ExitCode)
		}
	}
}
