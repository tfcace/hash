package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A script can trap the signals that end it, as in bash: `trap cleanup EXIT
// INT TERM` used to fail, which ended a set -e script before its work.
func TestLaunch_SignalTrapsAreAccepted(t *testing.T) {
	runHash(t, "", "", "-c", `set -euo pipefail; trap "echo cleaned" EXIT INT TERM; echo work`).want(t, "work\ncleaned\n", 0)
	runHash(t, "", "", "-c", `trap 'echo bye' exit; echo work`).want(t, "work\nbye\n", 0)
}

// The trap of the signal that ends a script runs first, with $? 128+signal,
// then the EXIT trap, and hash dies of the signal; an exit in the signal's
// trap ends hash with that status instead. bash would go on after a trap
// that does not exit; hash cannot pick a stopped line up again (README).
func TestLaunch_SignalTrapRunsWhenTheSignalEndsTheScript(t *testing.T) {
	t.Parallel()
	stdout, _, state, _ := signalHash(t, syscall.SIGTERM, "", "-c", `trap 'echo term $?' TERM; trap 'echo exit $?' EXIT; read x`)
	ws := state.Sys().(syscall.WaitStatus)
	if stdout != "term 143\nexit 143\n" || !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
		t.Errorf("TERM trap: stdout %q, ended %v; want term 143, exit 143 and killed by SIGTERM", stdout, ws)
	}

	stdout, _, state, _ = signalHash(t, syscall.SIGHUP, "", "-c", `trap 'echo hup; exit 3' hup; trap 'echo exit $?' EXIT; read x`)
	if stdout != "hup\nexit 3\n" || state.ExitCode() != 3 {
		t.Errorf("HUP trap that exits: stdout %q, status %d; want hup, exit 3, status 3", stdout, state.ExitCode())
	}

	out, ws := interruptHash(t, "-c", `trap 'echo int $?' INT; trap 'echo exit $?' EXIT; : > "$READY"; sleep 5; echo after`)
	if out != "int 130\nexit 130\n" || !ws.Signaled() || ws.Signal() != syscall.SIGINT {
		t.Errorf("INT trap: stdout %q, ended %v; want int 130, exit 130 and killed by SIGINT", out, ws)
	}
}

// Ctrl+\ (SIGQUIT) leaves a script running, as bash 5.2 ignores it in
// every shell: the foreground command dies of it, and a QUIT trap runs
// once the command's line ended, with $? its status. It used to end the
// script as a fatal signal (EXIT trap, status 131).
func TestLaunch_QuitLeavesAScriptRunning(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, trap, want string }{
		{"no trap", "", "after 131"},
		{"a QUIT trap", "trap 'echo quit-trap $?' QUIT\n", "quit-trap 131\r\nafter 131"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := filepath.Join(t.TempDir(), "s.sh")
			body := "trap 'echo bye' EXIT\n" + tc.trap + "echo ready; sleep 2\necho after $?\n"
			if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			s := startTTY(t, script)
			s.waitFor(0, "ready", 5*time.Second)
			time.Sleep(200 * time.Millisecond) // at the sleep
			s.typeString("\x1c")
			if state := s.wait(5 * time.Second); state.String() != "exit status 0" {
				t.Errorf("hash ended %v, want exit status 0", state)
			}
			if out := s.output(); !strings.Contains(out, tc.want+"\r\nbye") {
				t.Errorf("terminal %q, want %q, then the EXIT trap's bye", out, tc.want)
			}
		})
	}
}

// An empty trap ignores its signal, as in bash.
func TestLaunch_EmptyTrapIgnoresTheSignal(t *testing.T) {
	t.Parallel()
	runHash(t, "", "", "-c", `trap '' TERM; kill -TERM $$; sleep 0.3; echo after`).want(t, "after\n", 0)
	out, ws := interruptHash(t, "-c", `trap '' INT; : > "$READY"; sleep 1; echo after`)
	if out != "after\n" || !ws.Exited() || ws.ExitStatus() != 0 {
		t.Errorf("trap '' INT then Ctrl+C: stdout %q, ended %v; want after, status 0", out, ws)
	}
}

// exec replaces the script with its command: the EXIT trap does not run.
func TestLaunch_ExecSkipsTheExitTrap(t *testing.T) {
	runHash(t, "", "", "-c", `trap "echo bye" EXIT; exec echo hi`).want(t, "hi\n", 0)
}
