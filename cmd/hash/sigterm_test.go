package main

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// SIGTERM leaves an interactive hash running, as bash ignores it there:
// `kill 0` from a job or a pipeline's command (they share the shell's
// process group) and `kill -TERM $$` must not end the terminal's shell.
// SIGHUP still ends it (TestInteractive_HangupRunsTheExitTrap).
func TestInteractive_TermLeavesTheShellRunning(t *testing.T) {
	t.Parallel()
	s := startTTY(t)
	time.Sleep(1500 * time.Millisecond) // past startup, at the prompt
	s.signalHash(syscall.SIGTERM)
	for _, line := range []string{
		`sh -c 'trap "kill 0" EXIT; sleep 0.2' &`,
		`sh -c 'trap "kill 0" EXIT; sleep 0.2' | cat`,
		`kill -TERM $$`,
	} {
		s.typeString(line + "\r")
		time.Sleep(700 * time.Millisecond)
	}
	from := len(s.output())
	s.typeString("echo alive-$((40+2))\r")
	s.waitFor(from, "alive-42", 5*time.Second)
	s.typeString("exit\r")
	if state := s.wait(5 * time.Second); !state.Exited() || state.ExitCode() != 0 {
		t.Errorf("hash ended %v, want exit 0\nterminal: %q", state, s.output())
	}
}

// hash -i with a command ignores SIGTERM too, as bash -ic does; a plain
// script keeps it fatal (TestLaunch_HangupAndTermRunTheExitTrap). hash runs
// in a process group of its own: the job's kill 0 must not reach the test.
func TestLaunch_InteractiveCommandIgnoresTerm(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, hashBinary(t), "-ic", `sh -c 'trap "kill 0" EXIT; sleep 0.2' & sleep 1; echo ALIVE`)
	cmd.Env = isolatedEnv(t.TempDir())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.Output()
	if string(out) != "ALIVE\n" || err != nil {
		t.Errorf("stdout %q, %s; want ALIVE and exit 0", out, endedBy(cmd.ProcessState))
	}
}

// endOf gives the EXIT trap $? = 128+signal after a fatal signal and then
// dies of the signal whatever the trap did; otherwise hash exits with the
// shell's status or the trap's exit.
func TestEndOf(t *testing.T) {
	terminated, cancel := context.WithCancelCause(context.Background())
	cancel(signalError{syscall.SIGTERM})
	hungUp, cancelHup := context.WithCancelCause(context.Background())
	cancelHup(signalError{syscall.SIGHUP})
	tests := []struct {
		name       string
		ctx        context.Context
		status     int
		trapExit   int // -1: the trap does not exit
		trapStatus int // $? the trap must see
		want       shellEnd
	}{
		{"clean exit", context.Background(), 0, -1, 0, shellEnd{status: 0}},
		{"exit with a status", context.Background(), 3, -1, 3, shellEnd{status: 3}},
		{"the trap's exit", context.Background(), 3, 9, 3, shellEnd{status: 9}},
		{"SIGTERM", terminated, 0, -1, 143, shellEnd{status: 143, signal: syscall.SIGTERM}},
		{"SIGTERM, the trap exits", terminated, 2, 5, 143, shellEnd{status: 143, signal: syscall.SIGTERM}},
		{"SIGHUP", hungUp, 0, -1, 129, shellEnd{status: 129, signal: syscall.SIGHUP}},
	}
	// A signal that comes while the shell closes (running its EXIT trap)
	// still kills hash.
	late, cancelLate := context.WithCancelCause(context.Background())
	if got := endOf(late, 0, shellCloser{close: func(syscall.Signal, int) (int, bool) {
		cancelLate(signalError{syscall.SIGTERM})
		return 0, false
	}}); got != (shellEnd{status: 143, signal: syscall.SIGTERM}) {
		t.Errorf("signal while closing: endOf = %+v, want signal terminated", got)
	}
	// The signal's trap runs first; an exit in it ends the shell as an
	// exit does, so the EXIT trap's exit counts again and hash lives.
	var order []string
	trapped := func(exit int) shellCloser {
		return shellCloser{
			signalTrap: func(sig syscall.Signal, status int) (int, bool) {
				order = append(order, fmt.Sprintf("%v trap $?=%d", sig, status))
				return exit, exit >= 0
			},
			close: func(_ syscall.Signal, status int) (int, bool) {
				order = append(order, fmt.Sprintf("exit trap $?=%d", status))
				return 9, status == 3
			},
		}
	}
	if got := endOf(terminated, 0, trapped(-1)); got != (shellEnd{status: 143, signal: syscall.SIGTERM}) || fmt.Sprint(order) != "[terminated trap $?=143 exit trap $?=143]" {
		t.Errorf("signal trap that does not exit: endOf = %+v, ran %v", got, order)
	}
	order = nil
	if got := endOf(terminated, 0, trapped(3)); got != (shellEnd{status: 9}) || fmt.Sprint(order) != "[terminated trap $?=143 exit trap $?=3]" {
		t.Errorf("signal trap that exits 3: endOf = %+v, ran %v; want status 9 (the EXIT trap's exit)", got, order)
	}
	for _, tt := range tests {
		var sawStatus int
		got := endOf(tt.ctx, tt.status, shellCloser{close: func(_ syscall.Signal, status int) (int, bool) {
			sawStatus = status
			return tt.trapExit, tt.trapExit >= 0
		}})
		if got != tt.want || sawStatus != tt.trapStatus {
			t.Errorf("%s: endOf = %+v, trap saw $? = %d; want %+v, $? = %d", tt.name, got, sawStatus, tt.want, tt.trapStatus)
		}
	}
}
