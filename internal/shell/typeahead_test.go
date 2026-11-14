package shell

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// ptyShell is an interactive shell on a terminal of the test's: its
// stdin and stdout are PTYs, so a command it runs gets a PTY of its own,
// as at a real prompt. It returns the user's side of the keys.
func ptyShell(t *testing.T) (sh *Shell, keys *os.File) {
	t.Helper()
	sh = newShellForAgentCommand(t)
	tty, _, keys := openTTY(t)
	useTTY(sh, tty)
	master, slave, err := pty.Open()
	if err != nil {
		t.Skip(err)
	}
	saved := os.Stdout
	os.Stdout = slave
	go io.Copy(io.Discard, master) //nolint:errcheck // drain the terminal
	t.Cleanup(func() {
		os.Stdout = saved
		slave.Close()
		master.Close()
	})
	sh.executor.UseInteractiveTerminal()
	return sh, keys
}

// typeAt types keys on the user's side after d.
func typeAt(t *testing.T, keys *os.File, d time.Duration, typed string) {
	t.Helper()
	time.AfterFunc(d, func() { _, _ = keys.WriteString(typed) })
}

// promptLine reads the next line the prompt submits.
func promptLine(t *testing.T, sh *Shell) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	line, err := sh.readLineWithEditor(ctx)
	if err != nil {
		t.Fatalf("prompt read %q, %v; want a line", line, err)
	}
	return line
}

// A line typed across a command's end is one line, as in bash: `echo
// A1⏎echo B` typed during `sleep`, then `2X⏎` once it ended, runs echo A1
// and then echo B2X. The next prompt got the keys the command left unread
// only through the first Enter, so the tail typed after the end (`2X`)
// ran as a command of its own: `cd build && rm -rf *` split after the &&
// would run rm -rf * where the user stood.
func TestTypeahead_ALineTypedAcrossACommandsEndJoins(t *testing.T) {
	sh, keys := ptyShell(t)
	typeAt(t, keys, 300*time.Millisecond, "echo A1\recho B")
	if _, err := sh.runShellLine(context.Background(), "sleep 0.8"); err != nil {
		t.Fatal(err)
	}
	if _, err := keys.WriteString("2X\r"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"echo A1", "echo B2X"} {
		if got := promptLine(t, sh); got != want {
			t.Fatalf("the prompt got %q, want %q", got, want)
		}
	}
}

// Keys typed while a chpwd hook or an init command runs on a PTY, and left
// unread, are the next prompt's, as those of a line: only the line handed
// them on (runShellLine), so they were lost.
func TestTypeahead_KeysLeftByAHookOrAnInitCommandComeBack(t *testing.T) {
	for name, run := range map[string]func(*testing.T, *Shell){
		"chpwd hook": func(t *testing.T, sh *Shell) {
			sh.config.Shell.Hooks.Chpwd = []string{"sleep 0.8"}
			cdForHook(t, sh)
			sh.runChpwdHook(context.Background())
		},
		"init command": func(t *testing.T, sh *Shell) {
			sh.config.Shell.InitCommands = []string{"sleep 0.8"}
			if err := sh.runInitCommands(context.Background()); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			sh, keys := ptyShell(t)
			typeAt(t, keys, 300*time.Millisecond, "echo TYPED\r")
			run(t, sh)
			if got := promptLine(t, sh); got != "echo TYPED" {
				t.Errorf("the prompt got %q, want the line typed during the %s", got, name)
			}
		})
	}
}

// Lines pasted while a command runs all run, in order, once it ended.
func TestTypeahead_APasteDuringACommandRunsEveryLine(t *testing.T) {
	sh, keys := ptyShell(t)
	var paste strings.Builder
	// About 800 bytes: under -race the prompts after the command run slowly,
	// and a backlog over about 1 KB in the terminal's queue meets macOS's
	// rewrite of it when line mode comes back (a bug of the OS: bash too).
	// e2e's TestTypeahead_APasteDuringACommandRunsEveryLine pastes 300 lines.
	const lines = 90
	for i := range lines {
		fmt.Fprintf(&paste, "echo L%d\r", i)
	}
	typeAt(t, keys, 300*time.Millisecond, paste.String())
	if _, err := sh.runShellLine(context.Background(), "sleep 1"); err != nil {
		t.Fatal(err)
	}
	for i := range lines {
		if got, want := promptLine(t, sh), fmt.Sprintf("echo L%d", i); got != want {
			t.Fatalf("line %d: the prompt got %q, want %q", i, got, want)
		}
	}
}
