package shell

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/executor"
)

// interruptedLine runs line at the prompt, presses Ctrl+C once the line has
// created the file $STARTED, and returns the outcome. The line must end
// within a deadline; the test's own context stops it otherwise.
func interruptedLine(t *testing.T, line string) commandOutcome {
	t.Helper()
	return interruptedLineOn(t, nil, line)
}

// interruptedLineOn is interruptedLine in a shell whose stdin is tty (nil:
// the process's).
func interruptedLineOn(t *testing.T, tty *os.File, line string) commandOutcome {
	t.Helper()
	sh := &Shell{config: config.Default(), executor: executor.New(), stdin: tty}
	if tty != nil {
		sh.executor.UseInput(tty)
	}
	started := filepath.Join(t.TempDir(), "started")
	sh.executor.SetExportedEnv("STARTED", started)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan error, 1)
	go func() {
		var err error
		out := captureStdout(t, sh, func() { _, err = sh.runShellLine(ctx, line) })
		if strings.Contains(out, "after") {
			t.Errorf("%q went on after Ctrl+C: %q", line, out)
		}
		done <- err
	}()
	waitFor(t, func() bool { _, err := os.Stat(started); return err == nil })
	sh.interrupts.fire()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the line ended the shell: %v", err)
		}
	case <-time.After(3 * time.Second):
		stop()
		<-done
		t.Fatalf("Ctrl+C did not stop %q", line)
	}
	return sh.last
}

// Ctrl+C stops in-process work at the prompt, as in bash: a loop of
// builtins (no command in the foreground to take the signal) ends, and the
// line's status is 130. The shell dropped the SIGINT nothing claimed, so
// `while :; do :; done` could not be stopped.
func TestRunShellLine_CtrlCStopsInProcessWork(t *testing.T) {
	for _, line := range []string{
		`: >"$STARTED"; while :; do :; done`,
		`: >"$STARTED"; while :; do :; done; echo after`,
	} {
		if o := interruptedLine(t, line); o.exitCode != 130 {
			t.Errorf("%q ended %d, want 130", line, o.exitCode)
		}
	}
}

// Once the line is over, Ctrl+C is not the line's: the next line runs.
func TestRunShellLine_TheNextLineRunsAfterAnInterruptedOne(t *testing.T) {
	sh := &Shell{config: config.Default(), executor: executor.New()}
	ctx := context.Background()
	if _, err := sh.runShellLine(ctx, "true"); err != nil {
		t.Fatal(err)
	}
	sh.interrupts.fire() // between lines: nothing to stop
	out := captureStdout(t, sh, func() {
		if _, err := sh.runShellLine(ctx, "echo next"); err != nil {
			t.Error(err)
		}
	})
	if out != "next\n" || sh.last.exitCode != 0 {
		t.Errorf("next line printed %q, ended %d; want it run", out, sh.last.exitCode)
	}
}

// `read` at the terminal stops on Ctrl+C too, and leaves what is typed
// after it unread: the next line is the next prompt's, never the
// interrupted read's.
func TestRunShellLine_CtrlCStopsARead(t *testing.T) {
	t.Parallel()
	tty, _, typing := openTTY(t)
	if o := interruptedLineOn(t, tty, `: >"$STARTED"; read Y; echo after`); o.exitCode != 130 {
		t.Errorf("read ended %d, want 130", o.exitCode)
	}
	if _, err := typing.WriteString("next\n"); err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 16)
		n, _ := tty.Read(buf)
		got <- string(buf[:n])
	}()
	select {
	case line := <-got:
		if line != "next\n" {
			t.Errorf("after the read the terminal holds %q, want the next line", line)
		}
	case <-time.After(2 * time.Second):
		t.Error("the line typed after the interrupted read is gone")
	}
}
