package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// interruptHash runs hash with args in a process group of its own and,
// once the script has created the file $READY (its traps set, its command
// running), sends SIGINT to the whole group, as Ctrl+C at a terminal does
// to the foreground job. It returns hash's stdout and how it ended.
func interruptHash(t *testing.T, args ...string) (stdout string, ws syscall.WaitStatus) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, hashBinary(t), args...)
	cmd.WaitDelay = time.Second
	ready := filepath.Join(t.TempDir(), "ready")
	cmd.Env = append(isolatedEnv(t.TempDir()), "READY="+ready)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	for _, err := os.Stat(ready); err != nil; _, err = os.Stat(ready) {
		if ctx.Err() != nil {
			_ = cmd.Wait() // stop hash before reading what it wrote
			t.Fatalf("hash %q never got ready\nstdout: %q\nstderr: %q", args, out.String(), errb.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // the command it started is waiting
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
	_ = cmd.Wait()
	if ctx.Err() != nil {
		t.Fatalf("hash %q did not end after SIGINT\nstdout: %q\nstderr: %q", args, out.String(), errb.String())
	}
	return out.String(), cmd.ProcessState.Sys().(syscall.WaitStatus)
}

// Ctrl+C in a script ends it as in bash: the foreground command gets the
// SIGINT and decides. When it dies of it, or no command runs, hash runs
// the EXIT trap and dies of SIGINT; a command that handles it and exits
// lets the script go on. hash used to die at once, with no EXIT trap.
func TestLaunch_InterruptEndsAScriptAsBash(t *testing.T) {
	t.Parallel()
	script := filepath.Join(t.TempDir(), "s.sh")
	if err := os.WriteFile(script, []byte(`trap 'echo trap $?' EXIT`+"\n: > \"$READY\"\nsleep 5\necho after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	died := []struct {
		name string
		args []string
	}{
		{"command dies of it", []string{"-c", `trap 'echo trap $?' EXIT; : > "$READY"; sleep 5; echo after`}},
		{"script file", []string{script}},
		{"no command running", []string{"-c", `trap 'echo trap $?' EXIT; : > "$READY"; while :; do :; done`}},
	}
	for _, tc := range died {
		out, ws := interruptHash(t, tc.args...)
		if out != "trap 130\n" || !ws.Signaled() || ws.Signal() != syscall.SIGINT {
			t.Errorf("%s: stdout %q, ended %v; want trap 130 and killed by SIGINT", tc.name, out, ws)
		}
	}

	// A command that handles SIGINT (and exits by itself) leaves the script
	// running.
	catcher := `trap 'echo trap $?' EXIT; sh -c 'trap "echo caught; exit 3" INT; : > "$READY"; sleep 5 & wait'; echo after $?`
	out, ws := interruptHash(t, "-c", catcher)
	if out != "caught\nafter 3\ntrap 0\n" || !ws.Exited() || ws.ExitStatus() != 0 {
		t.Errorf("command handles it: stdout %q, ended %v; want caught, after 3, trap 0, status 0", out, ws)
	}
}

// A command dying of a SIGINT hash did not get (not Ctrl+C) does not end
// the script, as in bash.
func TestLaunch_CommandsOwnSIGINTDoesNotEndTheScript(t *testing.T) {
	runHash(t, "", "", "-c", `sh -c 'kill -INT $$'; echo after $?`).want(t, "after 130\n", 0)
}

// Ctrl+C at a terminal reaches every command of the script's foreground
// pipeline, and bash decides once they all ended: `sleep 10 | handler`
// runs the handler to its end, and the script goes on. hash used to end
// at sleep's death and kill the handler, which left the terminal as the
// handler had set it (`yes | less`: echo off, alternate screen on).
func TestLaunch_InterruptWaitsForTheWholePipeline(t *testing.T) {
	t.Parallel()
	handler := `trap "echo caught" INT; stty -echo </dev/tty; echo handler-ready; i=0; while [ $i -lt 10 ]; do sleep 0.05; i=$((i+1)); done; stty echo </dev/tty; echo done`
	s := startTTY(t, "-c", `sh -c 'echo left-ready >&2; exec sleep 10' | sh -c '`+handler+`'; echo after $?; stty -a | grep -o ' echo '`)
	s.waitFor(0, "left-ready", 5*time.Second)
	s.waitFor(0, "handler-ready", 5*time.Second)
	s.typeString("\x03")
	state := s.wait(10 * time.Second)
	out := s.output()
	if !strings.Contains(out, "caught") || !strings.Contains(out, "done") || !strings.Contains(out, "after 0") {
		t.Errorf("terminal %q, want the handler's caught and done, then after 0", out)
	}
	if !strings.Contains(out, " echo ") {
		t.Errorf("the terminal's echo was off after the pipeline: %q", out)
	}
	if !state.Exited() || state.ExitCode() != 0 {
		t.Errorf("hash ended %v, want exit 0", state)
	}
}

// A script's INT trap runs when Ctrl+C comes while a command that handles
// it runs, after that command's line, with $? its status, and the script
// goes on with $? as it was, as bash runs it after the command. hash used
// to skip the trap.
func TestLaunch_InterruptTrapRunsAfterTheCommandHandledIt(t *testing.T) {
	script := filepath.Join(t.TempDir(), "s.sh")
	body := `trap 'echo int-trap $?' INT
sh -c 'trap "echo caught; exit 3" INT; echo handler-ready; sleep 5 & wait'
echo after $?
`
	if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s := startTTY(t, script)
	s.waitFor(0, "handler-ready", 5*time.Second)
	s.typeString("\x03")
	state := s.wait(10 * time.Second)
	out := strings.ReplaceAll(s.output(), "\r", "")
	if !strings.Contains(out, "caught\nint-trap 3\nafter 3\n") || !state.Exited() || state.ExitCode() != 0 {
		t.Errorf("terminal %q, ended %v; want caught, int-trap 3, after 3 and exit 0", out, state)
	}
}

// The interactive shell runs no INT trap when Ctrl+C reached a command
// that handled it, as bash 5.2's does not; a script does (see
// TestLaunch_InterruptTrapRunsAfterTheCommandHandledIt).
func TestInteractive_InterruptRunsNoINTTrap(t *testing.T) {
	t.Parallel()
	s := startTTY(t)
	time.Sleep(1500 * time.Millisecond) // past startup, at the prompt
	s.typeString("trap 'echo int-$((6*7))' INT\r")
	from := len(s.output())
	s.typeString(`sh -c 'trap "echo caught" INT; echo ready-$((1+1)); sleep 1' | cat` + "\r")
	s.waitFor(from, "ready-2", 5*time.Second)
	s.typeString("\x03")
	s.waitFor(from, "caught", 5*time.Second)
	s.typeString("echo after-$((2+3))\r")
	s.waitFor(from, "after-5", 5*time.Second)
	if strings.Contains(s.output()[from:], "int-42") {
		t.Errorf("the interactive shell ran its INT trap: %q", s.output()[from:])
	}
	s.typeString("exit\r")
	s.wait(5 * time.Second)
}
