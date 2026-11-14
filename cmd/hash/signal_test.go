package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// waitExit waits for cmd under a hard deadline and returns its status.
func waitExit(t *testing.T, cmd *exec.Cmd, done <-chan error) int {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("hash did not exit on the signal")
	}
	return cmd.ProcessState.ExitCode()
}

// endedBy says how a process ended: "exit N" or "signal NAME".
func endedBy(state *os.ProcessState) string {
	if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return "signal " + ws.Signal().String()
	}
	return fmt.Sprintf("exit %d", state.ExitCode())
}

// SIGHUP (the terminal closed) and SIGTERM end a non-interactive hash the
// way they end bash 5: the EXIT trap runs, with $? = 128+signal in hash,
// and hash dies of the signal (make, timeout and a parent shell see a
// process the signal killed), whatever the trap does, while a command runs
// and while hash waits for its next line.
func TestLaunch_HangupAndTermRunTheExitTrap(t *testing.T) {
	t.Parallel()
	for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGTERM} {
		for _, script := range []string{"-c", "stdin"} {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			var cmd *exec.Cmd
			var stdin io.WriteCloser
			if script == "-c" {
				cmd = exec.CommandContext(ctx, hashBinary(t), "-c", `trap 'echo "bye $?"; exit 5' EXIT; sleep 10; echo no`)
			} else {
				cmd = exec.CommandContext(ctx, hashBinary(t))
				stdin, _ = cmd.StdinPipe()
			}
			cmd.Env = isolatedEnv(t.TempDir())
			var out bytes.Buffer
			cmd.Stdout = &out
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			if stdin != nil {
				_, _ = io.WriteString(stdin, "trap 'echo \"bye $?\"; exit 5' EXIT\n") // then hash waits for the next line
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			time.Sleep(500 * time.Millisecond)
			_ = cmd.Process.Signal(sig)
			waitExit(t, cmd, done)
			cancel()
			if stdin != nil {
				stdin.Close()
			}
			want := fmt.Sprintf("bye %d\n", 128+int(sig))
			if got := endedBy(cmd.ProcessState); got != "signal "+sig.String() || out.String() != want {
				t.Errorf("%v during %s: %s, stdout %q; want signal %v and %q", sig, script, got, out.String(), sig, want)
			}
		}
	}
}

// A bare exit in the EXIT trap exits with the status from before the trap.
func TestLaunch_BareExitInTheExitTrapKeepsTheStatus(t *testing.T) {
	runHash(t, "", "", "-c", `trap "echo x; exit" EXIT; (exit 7)`).want(t, "x\n", 7)
}

// The interactive shell runs its EXIT trap when the terminal hangs up, and
// dies of the hangup.
func TestInteractive_HangupRunsTheExitTrap(t *testing.T) {
	t.Parallel()
	marker := filepath.Join(t.TempDir(), "bye")
	cmd := exec.Command(hashBinary(t))
	cmd.Env = isolatedEnv(interactiveHome(t))
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	defer ptmx.Close()
	go io.Copy(io.Discard, ptmx) //nolint:errcheck // drain the terminal
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	time.Sleep(1500 * time.Millisecond) // past startup, at the prompt
	_, _ = ptmx.WriteString("trap 'echo bye > " + marker + "' EXIT\r")
	time.Sleep(300 * time.Millisecond)
	_ = cmd.Process.Signal(syscall.SIGHUP)
	waitExit(t, cmd, done)
	if got := endedBy(cmd.ProcessState); got != "signal hangup" {
		t.Errorf("hash ended with %s, want signal hangup", got)
	}
	if data, _ := os.ReadFile(marker); string(data) != "bye\n" {
		t.Errorf("EXIT trap wrote %q, want bye", data)
	}
}

// A SIGHUP hash was started with ignored stays ignored, as in bash:
// `nohup hash script.sh` must survive the terminal's hangup. (Go keeps
// such an ignore only for SIGHUP and SIGINT.)
func TestLaunch_IgnoredHangupStaysIgnored(t *testing.T) {
	t.Parallel()
	for _, args := range []string{`-c 'sleep 1; echo survived'`, ""} {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "trap '' HUP; exec "+hashBinary(t)+" "+args)
		cmd.Env = isolatedEnv(t.TempDir())
		var out bytes.Buffer
		cmd.Stdout = &out
		if args == "" {
			cmd.Stdin = strings.NewReader("sleep 1; echo survived\n")
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		time.Sleep(500 * time.Millisecond)
		_ = cmd.Process.Signal(syscall.SIGHUP)
		code := waitExit(t, cmd, done)
		cancel()
		if code != 0 || out.String() != "survived\n" {
			t.Errorf("hash %s with SIGHUP ignored, then hung up: status %d, stdout %q; want 0 and survived", args, code, out.String())
		}
	}
}

// The command running when hash gets a fatal signal gets it too, so it can
// clean up, as when the terminal hangs up on the whole job; it used to be
// killed outright.
func TestLaunch_RunningCommandGetsTheFatalSignal(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, hashBinary(t), "-c", `sh -c 'trap "echo cleanup; exit 1" HUP; while :; do sleep 0.05; done'`)
	cmd.Env = isolatedEnv(t.TempDir())
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	time.Sleep(500 * time.Millisecond)
	_ = cmd.Process.Signal(syscall.SIGHUP)
	waitExit(t, cmd, done)
	if out.String() != "cleanup\n" || endedBy(cmd.ProcessState) != "signal hangup" {
		t.Errorf("stdout %q, %s; want cleanup and signal hangup", out.String(), endedBy(cmd.ProcessState))
	}
}

// Ctrl+\ (SIGQUIT) does not end the interactive shell, as in bash, even
// while a line runs in hash (a read of the terminal, which keeps the
// terminal's signals on). Go's default for it used to end hash.
func TestInteractive_QuitKeyLeavesTheShellRunning(t *testing.T) {
	t.Parallel()
	s := startTTY(t)
	time.Sleep(1500 * time.Millisecond) // past startup, at the prompt
	from := len(s.output())
	s.typeString("read x\r")
	const lineRuns = "\x1b[?2004l" // the editor hands the terminal to the line
	s.waitFor(from, lineRuns, 3*time.Second)
	s.typeString("\x1c")
	s.typeString("v\r")
	from = len(s.output())
	s.typeString("echo got-$x\r")
	s.waitFor(from, "got-v", 5*time.Second)
	s.typeString("exit\r")
	if state := s.wait(5 * time.Second); !state.Exited() || state.ExitCode() != 0 {
		t.Errorf("hash ended %v, want exit 0", state)
	}
}
