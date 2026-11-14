package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// readOnTTY runs a read -p and a read -s on hash's terminal, as typed by
// a person: the prompt must show before the answer is typed, and the
// secret must not show as it is typed. from is where the session's output
// for the reads begins.
func readOnTTY(t *testing.T, name string, s *ttySession, from int) {
	t.Helper()
	s.waitFor(from, "Name: ", 3*time.Second)
	s.typeString("bob\r")
	s.waitFor(from, "hi bob", 3*time.Second)
	at := from + strings.Index(s.output()[from:], "hi bob")
	s.waitFor(at, "Pw: ", 3*time.Second)
	s.typeString("s3cret\r")
	s.waitFor(at, "got s3cret", 3*time.Second)
	if n := strings.Count(s.output()[at:], "s3cret"); n != 1 {
		t.Errorf("%s: the secret showed %d times on the terminal, want only in the echo of it\nterminal: %q", name, n, s.output()[at:])
	}
}

const readProgram = `read -p "Name: " n; echo "hi $n"; read -s -p "Pw: " pw; echo "got $pw"`

// read -p shows its prompt before it waits for the answer, and read -s
// reads without echo, in -c, a script and the interactive shell: a wait
// for input ahead of the read used to hold the prompt until the answer was
// typed, and to let the terminal echo the secret.
func TestLaunch_ReadPromptsAndHidesOnATerminal(t *testing.T) {
	t.Parallel()
	readOnTTY(t, "-c", startTTY(t, "-c", readProgram), 0)

	script := filepath.Join(t.TempDir(), "read.sh")
	if err := os.WriteFile(script, []byte(readProgram+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	readOnTTY(t, "script", startTTY(t, script), 0)

	s := startTTY(t)
	time.Sleep(1500 * time.Millisecond) // past startup, at the prompt
	from := len(s.output())
	s.typeString(readProgram + "\r")
	const lineRuns = "\x1b[?2004l" // the editor hands the terminal to the line
	s.waitFor(from, lineRuns, 3*time.Second)
	readOnTTY(t, "interactive", s, strings.LastIndex(s.output(), lineRuns))
	s.typeString("exit\r")
	s.wait(5 * time.Second)
}

// A fatal signal ends a read waiting on the terminal at once, with its
// echo back on after read -s.
func TestLaunch_SignalEndsAReadOnATerminal(t *testing.T) {
	s := startTTY(t, "-c", `trap "stty -a | grep -o ' echo '; echo bye" EXIT; read -s -p "Q: " x; echo after`)
	s.waitFor(0, "Q: ", 3*time.Second)
	_ = s.cmd.Process.Signal(syscall.SIGTERM)
	state := s.wait(3 * time.Second)
	if ws, ok := state.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
		t.Errorf("hash ended %v, want killed by SIGTERM", state)
	}
	out := s.output()
	if !strings.Contains(out, "bye") || strings.Contains(out, "after") {
		t.Errorf("terminal %q, want the EXIT trap's bye and no after", out)
	}
	if !strings.Contains(out, " echo ") {
		t.Errorf("the echo was still off in the EXIT trap after read -s: %q", out)
	}
}

// Ctrl+C at a read on the terminal ends a script at once (no command runs
// to decide), after its EXIT trap, and hash dies of SIGINT, as bash.
func TestLaunch_InterruptEndsAReadOnATerminal(t *testing.T) {
	s := startTTY(t, "-c", `trap "echo bye \$?" EXIT; read -p "Q: " x; echo after`)
	s.waitFor(0, "Q: ", 3*time.Second)
	s.typeString("\x03")
	state := s.wait(3 * time.Second)
	if ws, ok := state.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGINT {
		t.Errorf("hash ended %v, want killed by SIGINT\nterminal: %q", state, s.output())
	}
	if out := s.output(); !strings.Contains(out, "bye 130") || strings.Contains(out, "after") {
		t.Errorf("terminal %q, want the EXIT trap's bye 130 and no after", out)
	}
}

// Keys typed ahead of a command reach it, as in bash: `head -n1⏎hello⏎`
// typed at once gives head the line, and the prompt comes back.
func TestInteractive_TypeaheadReachesTheCommand(t *testing.T) {
	t.Parallel()
	s := startTTY(t)
	time.Sleep(1500 * time.Millisecond) // past startup, at the prompt
	from := len(s.output())
	s.typeString("head -n1\rhello\r")
	s.waitFor(from, "hello\r\nhello\r\n\x1b]133;D;0", 5*time.Second) // the echo, head's line, the line's end
	s.typeString("exit\r")
	s.wait(5 * time.Second)
}
