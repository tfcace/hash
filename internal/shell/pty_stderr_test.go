package shell

import (
	"io"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/executor"
)

func runExecResult(t *testing.T, s *Shell, line string, result *executor.Result) {
	t.Helper()
	cap := newStderrCapture(io.Discard)
	s.handleExecutionResult(line, result, nil, cap)
}

func TestPTYStderr_FallbackForFailedPTYCommand(t *testing.T) {
	s := &Shell{}
	merged := "\x1b]133;C\x07some output\r\n\x1b[31mcat: missing.txt: No such file or directory\x1b[0m\r\n"
	runExecResult(t, s, "cat missing.txt", &executor.Result{
		ExitCode:       1,
		UsedPTY:        true,
		CapturedOutput: merged,
	})

	if !strings.Contains(s.last.stderr, "cat: missing.txt: No such file or directory") {
		t.Errorf("lastStderr = %q, want the error text from merged PTY output", s.last.stderr)
	}
	if strings.Contains(s.last.stderr, "\x1b") {
		t.Errorf("lastStderr should have ANSI sequences stripped, got %q", s.last.stderr)
	}
	if strings.Contains(s.last.stderr, "\r") {
		t.Errorf("lastStderr should have CR normalized away, got %q", s.last.stderr)
	}
}

func TestPTYStderr_NoFallbackOnSuccess(t *testing.T) {
	s := &Shell{}
	runExecResult(t, s, "ls", &executor.Result{
		ExitCode:       0,
		UsedPTY:        true,
		CapturedOutput: "file1\r\nfile2\r\n",
	})

	if s.last.stderr != "" {
		t.Errorf("lastStderr = %q, want empty for a successful command", s.last.stderr)
	}
}

func TestPTYStderr_NoFallbackWithoutPTY(t *testing.T) {
	s := &Shell{}
	runExecResult(t, s, "ls", &executor.Result{
		ExitCode:       1,
		UsedPTY:        false,
		CapturedOutput: "this is stdout, not stderr",
	})

	if s.last.stderr != "" {
		t.Errorf("lastStderr = %q, want empty when stderr capture works normally", s.last.stderr)
	}
}

func TestPTYStderr_RealStderrWins(t *testing.T) {
	s := &Shell{}
	cap := newStderrCapture(io.Discard)
	_, _ = cap.Write([]byte("real stderr line"))
	s.handleExecutionResult("cmd", &executor.Result{
		ExitCode:       1,
		UsedPTY:        true,
		CapturedOutput: "merged output",
	}, nil, cap)

	if s.last.stderr != "real stderr line" {
		t.Errorf("lastStderr = %q, want the directly captured stderr", s.last.stderr)
	}
}

func TestPTYStderr_KeepsTailOfLongOutput(t *testing.T) {
	s := &Shell{}
	long := strings.Repeat("early filler line\r\n", 2000) + "final error: it broke\r\n"
	runExecResult(t, s, "build", &executor.Result{
		ExitCode:       2,
		UsedPTY:        true,
		CapturedOutput: long,
	})

	if !strings.Contains(s.last.stderr, "final error: it broke") {
		t.Error("the tail (where errors print) must be kept")
	}
	if len(s.last.stderr) > maxStderrCapture {
		t.Errorf("lastStderr length %d exceeds cap %d", len(s.last.stderr), maxStderrCapture)
	}
}

func TestStripTerminalSequences(t *testing.T) {
	in := "\x1b]0;title\x07plain \x1b[1;31mred\x1b[0m \x1b[2Kcleared\x1b[3A moved"
	got := stripTerminalSequences(in)
	want := "plain red cleared moved"
	if got != want {
		t.Errorf("stripTerminalSequences() = %q, want %q", got, want)
	}
}
