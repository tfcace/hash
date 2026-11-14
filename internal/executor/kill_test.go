package executor

import (
	"bytes"
	"context"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The interpreter has no kill builtin ("kill: unsupported builtin", status
// 2, in every mode): kill runs the system's kill command, unless the user
// defined a kill function.
func TestExecute_KillRunsTheKillCommand(t *testing.T) {
	e := New()
	var out bytes.Buffer
	if _, err := e.Execute(context.Background(), `kill -0 $$; echo "self=$?"; kill -0 999999 2>/dev/null; echo "gone=$?"; kill() { echo "func $1"; }; kill x`, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if want := "self=0\ngone=1\nfunc x\n"; out.String() != want {
		t.Fatalf("stdout = %q, want %q", out.String(), want)
	}
}

// `command kill` and `builtin kill` run kill too, past a kill function, as
// in bash; they used to reach the interpreter's missing builtin.
func TestExecute_PrefixedKillRunsTheKillCommand(t *testing.T) {
	e := New()
	var out bytes.Buffer
	if _, err := e.Execute(context.Background(), `kill() { echo fn; }; command kill -0 $$; echo "c=$?"; builtin kill -0 $$; echo "b=$?"`, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if want := "c=0\nb=0\n"; out.String() != want {
		t.Fatalf("stdout = %q, want %q", out.String(), want)
	}
}

// `command exec` replaces the shell as exec does: its EXIT trap goes.
func TestExecute_PrefixedExecDropsTheExitTrap(t *testing.T) {
	e := New()
	execLine(t, e, `trap 'echo bye' EXIT; command exec true`)
	var out bytes.Buffer
	e.RunExitTrap(context.Background(), &out, io.Discard)
	if out.Len() != 0 {
		t.Fatalf("the EXIT trap ran after command exec: %q", out.String())
	}
}

// kill takes a job, by %N, %%, gN or $!, as bash does: it ends the job's
// part hash runs itself (a runaway `while :; do :; done &`) and signals
// the commands it started; wait then reports 128+signal. A job used to
// have no pid kill could reach.
func TestExecute_KillStopsAJob(t *testing.T) {
	t.Parallel()
	tests := []struct{ line, want string }{
		{`while :; do :; done & kill $!; wait $!; echo "st=$?"`, "st=143\n"},
		{`sleep 10 & kill %1; wait %1; echo "st=$?"`, "st=143\n"},
		{`sleep 10 & sleep 0.2; kill -9 %%; wait; echo "st=$?"`, "st=0\n"},
		{`{ sleep 10; echo never; } & sleep 0.2; kill -s KILL g1; wait g1; echo "st=$?"`, "st=137\n"},
		{`while :; do :; done & p=$!; sleep 0.1; kill -0 $p; echo "alive=$?"; kill $p; wait $p; echo "st=$?"`, "alive=0\nst=143\n"},
		{`kill %9; echo "st=$?"`, "st=1\n"},
		// Each job of a loop is its own: kill takes the one named.
		{`for i in 1 2 3; do sleep 10 & done; kill %2; wait %2; echo "two=$?"; kill -0 %1 %3; echo "others=$?"; kill %1 %3; wait`, "two=143\nothers=0\n"},
	}
	for _, tc := range tests {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var out, errb bytes.Buffer
		_, err := New().Execute(ctx, tc.line, &out, &errb)
		cancel()
		if err != nil || out.String() != tc.want {
			t.Errorf("%q: out %q, stderr %q, err %v; want %q", tc.line, out.String(), errb.String(), err, tc.want)
		}
	}
}

// pidIn waits for path to hold a pid and returns it.
func pidIn(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no pid in %s", path)
	return 0
}

// gone reports whether pid is gone within a deadline (a zombie counts as
// running: the test binary reaps only its own children).
func gone(pid int) bool {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	return false
}

// kill %N and the shell's hangup reach a job's whole process group, as in
// bash with job control: a command the job's command started dies with it,
// and so do the jobs a job started. They used to signal the job's own
// commands only: `sh -c 'sleep 30 & wait' &` left the sleep running.
func TestExecute_KillAndHangupReachTheJobsChildren(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	e := New()
	e.terminal = terminalInteractive
	var out, errb bytes.Buffer
	line := `sh -c 'sleep 30 & echo $! > c1; wait' & { sh -c 'echo $$ > c2; exec sleep 30' & wait; } & sh -c 'sleep 30 & echo $! > c3; wait' &`
	res, err := e.Execute(context.Background(), line, &out, &errb)
	if err != nil {
		t.Fatal(err)
	}
	c1, c2, c3 := pidIn(t, "c1"), pidIn(t, "c2"), pidIn(t, "c3")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := e.Execute(ctx, "kill %1 %2; wait %1 %2", &out, &errb); err != nil {
		t.Fatal(err)
	}
	if g1, g2 := gone(c1), gone(c2); !g1 || !g2 {
		t.Errorf("kill %%1 %%2 left a child of the jobs running (c1 gone %v, c2 gone %v)", g1, g2)
	}
	e.SignalJobs(syscall.SIGHUP)
	if !gone(c3) {
		t.Error("the hangup left the child of a job running")
	}
	if res.JobsDone != nil {
		select {
		case <-res.JobsDone:
		case <-time.After(5 * time.Second):
			t.Fatal("the jobs never ended")
		}
	}
}

// The interactive shell's job's command leads a process group of its own,
// so the terminal's Ctrl+C, Ctrl+\ and hangup at the shell's exit reach
// only the shell's foreground, and `kill 0` in it stays in it. A script's
// shares the script's, as in bash with no job control.
func TestExecute_JobLeadsAProcessGroup(t *testing.T) {
	for _, mode := range []terminalMode{terminalInteractive, terminalPlain} {
		e := New()
		e.terminal = mode
		var out bytes.Buffer
		if _, err := e.Execute(context.Background(), `sh -c 'echo $$ $(ps -o pgid= -p $$)' & wait`, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
		f := strings.Fields(out.String())
		own := len(f) == 2 && f[0] == f[1] && f[1] != strconv.Itoa(syscall.Getpgrp())
		shared := len(f) == 2 && f[1] == strconv.Itoa(syscall.Getpgrp())
		if (mode == terminalInteractive && !own) || (mode == terminalPlain && !shared) {
			t.Errorf("mode %d: pid and pgid %q (hash's group %d); want a group of its own interactive, hash's in a script", mode, out.String(), syscall.Getpgrp())
		}
	}
}

// kill and wait take %%, %+ and %- over the running jobs, the ones jobs
// marks + and - (%- the one job when there is one, as in bash): they named the newest and the one before it of every job
// the shell ever started, so once the newest ended `kill %%` said "no such
// job" while jobs marked another +, and `wait %%` returned at once.
func TestExecute_CurrentJobIsTheOneJobsMarks(t *testing.T) {
	t.Parallel()
	tests := []struct{ line, want string }{
		{`sleep 5 & true & sleep 0.2; jobs; kill %%; echo "k=$?"; wait %1; echo "st=$?"`,
			"[1]+  Running                 sleep 5 &\nk=0\nst=143\n"},
		{`sleep 5 & sleep 5 & true & sleep 0.2; kill %-; echo "k=$?"; wait %1; echo "st=$?"; jobs; kill %%`,
			"k=0\nst=143\n[2]+  Running                 sleep 5 &\n"},
		{`(sleep 0.3; exit 3) & true & sleep 0.1; wait %%; echo "w=$?"`, "w=3\n"},
		// With one job %- names it too, as bash's.
		{`sleep 5 & sleep 0.1; kill %-; echo "k=$?"; wait %1; echo "w=$?"`, "k=0\nw=143\n"},
	}
	for _, tc := range tests {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var out, errb bytes.Buffer
		_, err := New().Execute(ctx, tc.line, &out, &errb)
		cancel()
		if err != nil || out.String() != tc.want {
			t.Errorf("%q: out %q, stderr %q, err %v; want %q", tc.line, out.String(), errb.String(), err, tc.want)
		}
	}
}
