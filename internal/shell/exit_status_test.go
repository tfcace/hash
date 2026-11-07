package shell

import (
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/config"
)

// exit ends the shell wherever it runs on the line, and hash exits with its
// status: `exit N`, a bare exit after a failure, or an exit inside a compound
// line all behave as in bash.
func TestExit_EndsTheShellWithItsStatus(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  int
	}{
		{"exit with a status", []string{"exit 3"}, 3},
		{"bare exit keeps the last status", []string{"false", "exit"}, 1},
		{"exit after a success", []string{"true", "exit"}, 0},
		{"exit inside a compound line", []string{"true && exit 4"}, 4},
		{"exit in a list", []string{"echo hi > /dev/null; exit 5"}, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sh, _, _ := newTurnShell(t, &scriptedTransport{})
			var err error
			for _, line := range tt.lines {
				err = sh.dispatchCommand(context.Background(), line)
			}
			var exit shellExit
			if !errors.As(err, &exit) {
				t.Fatalf("last line returned %v, want a shellExit", err)
			}
			if exit.status != tt.want {
				t.Fatalf("exit status = %d, want %d", exit.status, tt.want)
			}
		})
	}
}

// An exit inside a subshell ends only the subshell.
func TestExit_InASubshellDoesNotEndTheShell(t *testing.T) {
	sh, _, _ := newTurnShell(t, &scriptedTransport{})
	if err := sh.dispatchCommand(context.Background(), "(exit 6)"); endsShell(err) {
		t.Fatal("(exit 6) ended the shell")
	}
	if sh.last.exitCode != 6 {
		t.Fatalf("line exit = %d, want 6", sh.last.exitCode)
	}
}

// Ctrl+D (or the input closing) ends the shell with the last line's status,
// as in bash and zsh; it used to exit 0 after a failure.
// Not parallel: it swaps os.Stdin and os.Stdout.
func TestExit_EOFEndsTheShellWithTheLastStatus(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  int
	}{
		{"false\n", 1},
		{"(exit 7)\n", 7},
		{"true\n", 0}, // one line each: lines typed ahead into one pipe can lose bytes between editors (editor area)
	} {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		w.WriteString(tt.input)
		w.Close()
		null, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		oldIn, oldOut := os.Stdin, os.Stdout
		os.Stdin, os.Stdout = r, null

		cfg := config.Default()
		cfg.History.Enabled = false
		cfg.Prompt.Mode = "none"
		sh, err := NewWithMode(cfg, Mode{})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		runErr := sh.Run(ctx)
		cancel()
		sh.Close() //nolint:errcheck // test cleanup
		os.Stdin, os.Stdout = oldIn, oldOut
		r.Close()
		null.Close()

		if runErr != nil {
			t.Fatalf("%q: Run() = %v", tt.input, runErr)
		}
		if got := sh.ExitStatus(); got != tt.want {
			t.Errorf("%q then EOF: ExitStatus() = %d, want %d", tt.input, got, tt.want)
		}
	}
}

// An exit in init_commands ends the shell with its status whether hash runs
// it as its builtin (`exit 3`) or the interpreter does (`true && exit 4`);
// the interpreter's exit used to be dropped and the shell started anyway.
func TestExit_InInitCommandsEndsTheShell(t *testing.T) {
	for line, want := range map[string]int{"exit 3": 3, "true && exit 4": 4} {
		sh, _, _ := newTurnShell(t, &scriptedTransport{})
		sh.config.Shell.InitCommands = []string{line, "echo not reached"}
		var exit shellExit
		if err := sh.runInitCommands(context.Background()); !errors.As(err, &exit) || exit.status != want {
			t.Errorf("init %q: runInitCommands() = %v, want exit %d", line, err, want)
		}
	}
}

// The EXIT trap runs with $? the status the shell exits with, as in bash
// and zsh: `exit 4` after a failure is t=4, not the failure's 1. Run ends
// on the exit's shellExit (ended) and Close runs the trap.
func TestExit_TheExitTrapSeesTheExitStatus(t *testing.T) {
	for _, tt := range []struct {
		lines []string
		want  string
	}{
		{[]string{"false", "exit 4"}, "t=4"},
		{[]string{"false", "true && exit 5"}, "t=5"},
		{[]string{"false", "exit"}, "t=1"},
	} {
		sh, _, _ := newTurnShell(t, &scriptedTransport{})
		out := captureStdout(t, sh, func() {
			_ = sh.handleLine(context.Background(), "trap 'echo t=$?' EXIT")
			var err error
			for _, line := range tt.lines {
				err = sh.handleLine(context.Background(), line)
			}
			if err := sh.ended(err); err != nil {
				t.Errorf("%v: ended() = %v", tt.lines, err)
			}
			_ = sh.Close()
		})
		if !strings.Contains(out, tt.want) {
			t.Errorf("%v: the EXIT trap printed %q, want %s", tt.lines, out, tt.want)
		}
	}
}

// A fatal signal (SIGTERM, SIGHUP) ends the REPL with 128+signal: the EXIT
// trap sees it as $?, not the last line's status, and it is the status the
// shell exits with unless the trap exits itself.
func TestExit_CloseOnSignalGivesTheTrapTheSignalStatus(t *testing.T) {
	for _, tt := range []struct {
		trap, want string
		status     int
	}{
		{"echo t=$?", "t=143", 143},
		{"echo t=$?; exit 5", "t=143", 5},
	} {
		sh, _, _ := newTurnShell(t, &scriptedTransport{})
		out := captureStdout(t, sh, func() {
			_ = sh.handleLine(context.Background(), "trap '"+tt.trap+"' EXIT")
			_ = sh.handleLine(context.Background(), "false")
			_ = sh.CloseOnSignal(syscall.SIGTERM)
		})
		if !strings.Contains(out, tt.want) || sh.ExitStatus() != tt.status {
			t.Errorf("trap %q: printed %q, ExitStatus() = %d; want %s and %d", tt.trap, out, sh.ExitStatus(), tt.want, tt.status)
		}
	}
}

// The status the shell exits with is decided as bash decides it: how Run
// ended (exit N) or the fatal signal (128+signal) sets it, then an exit in
// the EXIT trap replaces it, and a trap that does not exit keeps it.
func TestExit_ExitStatusAfterClose(t *testing.T) {
	for _, tt := range []struct {
		name, trap string
		signal     syscall.Signal
		want       int
	}{
		{"exit, trap goes on", "echo t", 0, 3},
		{"exit, trap exits", "exit 5", 0, 5},
		{"signal, trap goes on", "echo t", syscall.SIGHUP, 129},
		{"signal, trap exits", "exit 6", syscall.SIGHUP, 6},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sh, _, _ := newTurnShell(t, &scriptedTransport{})
			captureStdout(t, sh, func() {
				_ = sh.handleLine(context.Background(), "trap '"+tt.trap+"' EXIT")
				if tt.signal != 0 {
					_ = sh.CloseOnSignal(tt.signal)
					return
				}
				_ = sh.ended(sh.handleLine(context.Background(), "exit 3"))
				if got := sh.ExitStatus(); got != 3 {
					t.Errorf("ExitStatus() before Close = %d, want 3", got)
				}
				_ = sh.Close()
			})
			if got := sh.ExitStatus(); got != tt.want {
				t.Errorf("ExitStatus() = %d, want %d", got, tt.want)
			}
		})
	}
}
