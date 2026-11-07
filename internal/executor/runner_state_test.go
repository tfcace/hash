package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// The contract tests for runner_state.go: each drives the interpreter
// state hash reaches into the way hash relies on it. A failure after a
// mvdan.cc/sh update means runner_state.go needs updating.

func newTestRunner(t *testing.T, opts ...interp.RunnerOption) (*interp.Runner, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	r, err := interp.New(append([]interp.RunnerOption{interp.StdIO(nil, &out, &out)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return r, &out
}

func runOn(t *testing.T, r *interp.Runner, name, src string) error {
	t.Helper()
	prog, err := syntax.NewParser().Parse(strings.NewReader(src), name)
	if err != nil {
		t.Fatal(err)
	}
	return r.Run(context.Background(), prog)
}

// The script name is $0, and hash names a nested program after it.
func TestRunnerState_ScriptNameIsDollarZero(t *testing.T) {
	r, out := newTestRunner(t)
	_ = runOn(t, r, "myscript", `echo "$0"`)
	name, ok := runnerScriptName(r)
	if !ok || name != "myscript" || out.String() != "myscript\n" {
		t.Fatalf("runnerScriptName = %q, %v; $0 printed %q; want myscript (update runner_state.go)", name, ok, out.String())
	}
}

// The ERR trap action is what `trap ... ERR` set, and "" once reset.
func TestRunnerState_ErrTrapIsTheTrapsAction(t *testing.T) {
	r, _ := newTestRunner(t)
	_ = runOn(t, r, "", `trap 'echo oops' ERR`)
	if got := runnerErrTrap(r); got != "echo oops" {
		t.Fatalf("runnerErrTrap = %q, want echo oops (update runner_state.go)", got)
	}
	_ = runOn(t, r, "", `trap - ERR`)
	if got := runnerErrTrap(r); got != "" {
		t.Fatalf("runnerErrTrap after reset = %q, want none", got)
	}
}

// Setting the last status is $? for the next command and for a bare exit,
// whatever failed before it; reading it is $?.
func TestRunnerState_SetLastStatusIsDollarQuestionAndBareExit(t *testing.T) {
	r, out := newTestRunner(t)
	_ = runOn(t, r, "", `false`)
	setRunnerLastStatus(r, 0)
	if err := r.Run(context.Background(), mustStmt(t, `exit`)); err != nil {
		t.Fatalf("exit after setting 0 after false: %v (update runner_state.go)", err)
	}
	r.Reset()
	_ = runOn(t, r, "", `false`)
	setRunnerLastStatus(r, 5)
	if err := r.Run(context.Background(), mustStmt(t, `echo "$?"`)); err != nil || out.String() != "5\n" {
		t.Fatalf("$? printed %q (%v), want 5 (update runner_state.go)", out.String(), err)
	}
	if code, ok := runnerLastStatus(r); !ok || code != 0 {
		t.Fatalf("runnerLastStatus after echo = %d, %v; want 0 (update runner_state.go)", code, ok)
	}
	setRunnerLastStatus(r, 6)
	if code, _ := runnerLastStatus(r); code != 6 {
		t.Fatalf("runnerLastStatus = %d, want the 6 just set", code)
	}
	var status interp.ExitStatus
	if err := r.Run(context.Background(), mustStmt(t, `exit`)); !errors.As(err, &status) || status != 6 {
		t.Fatalf("bare exit = %v, want status 6 (update runner_state.go)", err)
	}
}

func mustStmt(t *testing.T, src string) *syntax.Stmt {
	t.Helper()
	prog, err := syntax.NewParser().Parse(strings.NewReader(src), "")
	if err != nil || len(prog.Stmts) != 1 {
		t.Fatalf("parse %q: %v", src, err)
	}
	return prog.Stmts[0]
}

// A handler call names the runner that made it: the shell's own at the
// top level, a different one in a subshell.
func TestRunnerState_HandlerRunnerIsTheCallingShell(t *testing.T) {
	var seen []*interp.Runner
	r, _ := newTestRunner(t, interp.CallHandler(func(ctx context.Context, args []string) ([]string, error) {
		if args[0] == "probe" {
			seen = append(seen, handlerRunner(interp.HandlerCtx(ctx)))
			return []string{":"}, nil
		}
		return args, nil
	}))
	_ = runOn(t, r, "", `probe; (probe); x=$(probe)`)
	if len(seen) != 3 || seen[0] != r || seen[1] == nil || seen[1] == r || seen[2] == nil || seen[2] == r {
		t.Fatalf("handler runners = %p (shell %p), want the shell then two subshells (update runner_state.go)", seen, r)
	}
}

// Each background job is one done channel, in $! order, closed once the
// job ended.
func TestRunnerState_JobsCloseWhenTheyEnd(t *testing.T) {
	gate := make(chan struct{})
	r, _ := newTestRunner(t, interp.ExecHandlers(func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
		return func(ctx context.Context, args []string) error {
			if args[0] == "block" {
				<-gate
				return nil
			}
			return next(ctx, args)
		}
	}))
	if err := runOn(t, r, "", "true & block & [ $! = g2 ]"); err != nil {
		t.Fatalf("$! after two jobs is not g2: %v", err)
	}
	jobs, ok := runnerJobs(r)
	if !ok || jobs.count() != 2 || len(jobs.all()) != 2 {
		t.Fatalf("runnerJobs = %d jobs, %v; want 2 (update runner_state.go)", jobs.count(), ok)
	}
	<-jobs.done(1)
	select {
	case <-jobs.done(2):
		t.Fatal("the blocked job's channel closed before it ended")
	default:
	}
	close(gate)
	<-jobs.done(2)
}

// errexit reads as set -e and set +e left it, in the shell and in a
// function the shell calls.
func TestRunnerState_ErrExitIsSetMinusE(t *testing.T) {
	r, _ := newTestRunner(t)
	if runnerErrExit(r) {
		t.Fatal("errexit on in a new runner")
	}
	_ = runOn(t, r, "", "set -e")
	if !runnerErrExit(r) {
		t.Fatal("errexit off after set -e (update runner_state.go)")
	}
	_ = runOn(t, r, "", "set +e; set -u")
	if runnerErrExit(r) {
		t.Fatal("errexit on after set +e")
	}
}

// A runner is in a function or a sourced file while it runs one, and in
// neither at its top level.
func TestRunnerState_InFunctionOrSource(t *testing.T) {
	var seen []string
	r, _ := newTestRunner(t, interp.CallHandler(func(ctx context.Context, args []string) ([]string, error) {
		if args[0] == "probe" {
			in, ok := runnerInFunctionOrSource(handlerRunner(interp.HandlerCtx(ctx)))
			seen = append(seen, fmt.Sprint(in, ok))
		}
		return args, nil
	}))
	_ = runOn(t, r, "", `probe; f() { probe; }; f; eval probe`)
	if got := strings.Join(seen, " "); got != "false true true true false true" {
		t.Fatalf("in function or source = %q, want top level false, function true, eval false (update runner_state.go)", got)
	}
}
