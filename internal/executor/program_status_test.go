package executor

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/tfcace/hash/internal/programstatus"
)

func newStatusExecutor(t *testing.T, threshold time.Duration) (*Executor, *bytes.Buffer) {
	t.Helper()
	e := New()
	e.progressThreshold = threshold
	var buf bytes.Buffer
	e.status = programstatus.NewReporter(&buf)
	return e, &buf
}

func cmdReport(state programstatus.State, app, msg string) string {
	return programstatus.Sequence(programstatus.Report{State: state, ID: "cmd", App: app, Msg: msg})
}

func TestExecute_LongCommandReportsWorkingThenDone(t *testing.T) {
	t.Parallel()
	e, buf := newStatusExecutor(t, 10*time.Millisecond)
	if _, err := e.Execute(context.Background(), "sleep 0.2", io.Discard, io.Discard); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := cmdReport(programstatus.Working, "sleep", "sleep 0.2") + cmdReport(programstatus.Done, "sleep", "sleep 0.2")
	if buf.String() != want {
		t.Errorf("wrote %q\nwant  %q", buf.String(), want)
	}
}

func TestExecute_LongFailingCommandReportsError(t *testing.T) {
	t.Parallel()
	e, buf := newStatusExecutor(t, 10*time.Millisecond)
	_, _ = e.Execute(context.Background(), "sleep 0.2; false", io.Discard, io.Discard)
	if want := cmdReport(programstatus.Error, "sleep", "sleep 0.2; false (exit 1)"); !strings.HasSuffix(buf.String(), want) {
		t.Errorf("wrote %q\nwant it to end with %q", buf.String(), want)
	}
}

func TestExecute_QuickCommandReportsNothing(t *testing.T) {
	e, buf := newStatusExecutor(t, time.Second)
	if _, err := e.Execute(context.Background(), "true", io.Discard, io.Discard); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("quick command wrote %q", buf.String())
	}
}

// go test's stdout is not a terminal, so neither escape sequence may be on.
func TestNew_StatusAndProgressNeedATerminal(t *testing.T) {
	e := New()
	if e.status.Enabled() {
		t.Error("status reports should be off when stdout is not a terminal")
	}
	if e.progressOSC.Enabled() {
		t.Error("the progress bar should be off when stdout is not a terminal")
	}
}

// Only the interactive shell drives the terminal: a plain executor (a
// script, -c) runs a command on hash's own terminal, never on a PTY of its
// own, so `tty` names hash's terminal; the interactive one gives the
// command a PTY.
func TestExecute_PlainExecutorRunsCommandsOnHashsTerminal(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()
	go io.Copy(io.Discard, ptmx) //nolint:errcheck // drain the terminal
	dup, err := syscall.Dup(int(tty.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	stdin := os.NewFile(uintptr(dup), "stdin")
	defer stdin.Close()
	origStdin := os.Stdin
	os.Stdin = stdin
	defer func() { os.Stdin = origStdin }()

	run := func(e *Executor) *Result {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res, err := e.Execute(ctx, "tty", tty, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	plain := run(New())
	if got := strings.TrimSpace(strings.ReplaceAll(plain.CapturedOutput, "\r", "")); got != tty.Name() || plain.UsedPTY {
		t.Errorf("plain executor: tty printed %q (PTY %v), want hash's terminal %s", got, plain.UsedPTY, tty.Name())
	}
	interactive := New()
	interactive.terminal = terminalInteractive
	if res := run(interactive); !res.UsedPTY {
		t.Errorf("interactive executor: tty ran without a PTY")
	}
}

// A plain line with no capture hands a command the line's own files, so a
// terminal stays a terminal on stdout and stderr (and with 2>&1); with a
// capture the command writes into the capture instead.
func TestExecute_PlainLineGivesCommandsItsFiles(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()
	var term bytes.Buffer
	copied := make(chan struct{})
	go func() {
		_, _ = io.Copy(&term, ptmx)
		close(copied)
	}()
	e := New()
	e.SetCaptureLimit(0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	probe := `sh -c '[ -t 1 ] && echo out-tty; [ -t 2 ] && echo err-tty >&2'; sh -c '[ -t 2 ] && echo both-tty >&2' 2>&1`
	if _, err := e.Execute(ctx, probe, tty, tty); err != nil {
		t.Fatal(err)
	}
	captured := New()
	res, err := captured.Execute(ctx, `sh -c '[ -t 1 ] || echo captured'`, tty, tty)
	if err != nil {
		t.Fatal(err)
	}
	tty.Close()
	select {
	case <-copied:
	case <-time.After(2 * time.Second):
	}
	for _, want := range []string{"out-tty", "err-tty", "both-tty"} {
		if !strings.Contains(term.String(), want) {
			t.Errorf("terminal %q, want %s", term.String(), want)
		}
	}
	if !strings.Contains(res.CapturedOutput, "captured") {
		t.Errorf("captured line: capture %q, want the command's output", res.CapturedOutput)
	}
}
