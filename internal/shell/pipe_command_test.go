package shell

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/agent"
)

// The command of `cmd | ?? prompt` is a line of the shell, as one at the
// prompt is: a job it starts runs on after the turn ends. It ran on the
// turn's context, so the turn's end cut the job off.
func TestPipeCommand_AJobItStartsOutlivesTheTurn(t *testing.T) {
	t.Parallel()
	tr := &scriptedTransport{scripts: []turnScript{{events: []agent.StreamEvent{textEvent("It printed x.")}}}}
	sh, _, _ := newTurnShell(t, tr)
	out := filepath.Join(t.TempDir(), "f")
	sh.executor.SetExportedEnv("OUT", out)
	captureStdout(t, sh, func() {
		if err := sh.handleLine(context.Background(), `{ sleep 0.3; echo hi >"$OUT"; } & echo x | ?? summarize`); err != nil {
			t.Error(err)
		}
	})
	if n := tr.requestCount(); n != 1 {
		t.Fatalf("%d requests, want the one with what the command printed", n)
	}
	if sh.turn.Load() != nil {
		t.Fatal("the turn is still open")
	}
	waitFor(t, func() bool { _, err := os.Stat(out); return err == nil })
	joinJobs(t, sh)
}

// joinJobs waits for the jobs the shell's lines started to end: a job
// still starting its command when the next test swaps os.Stdin races it.
func joinJobs(t *testing.T, sh *Shell) {
	t.Helper()
	sh.executor.LetJobsStart(5 * time.Second)
}

// Ctrl+C while the command of `cmd | ?? prompt` runs interrupts it as a
// line at the prompt: the turn sends nothing, and a job the command
// started runs on.
func TestPipeCommand_CtrlCInterruptsItAsALine(t *testing.T) {
	t.Parallel()
	tr := &scriptedTransport{}
	sh, _, _ := newTurnShell(t, tr)
	dir := t.TempDir()
	out, started := filepath.Join(dir, "f"), filepath.Join(dir, "started")
	sh.executor.SetExportedEnv("OUT", out)
	sh.executor.SetExportedEnv("STARTED", started)
	go func() {
		waitFor(t, func() bool { _, err := os.Stat(started); return err == nil })
		sh.interrupts.fire()
	}()
	captureStdout(t, sh, func() {
		_ = sh.handleLine(context.Background(), `{ sleep 0.3; echo hi >"$OUT"; } & : >"$STARTED"; while :; do :; done | ?? summarize`)
	})
	if n := tr.requestCount(); n != 0 {
		t.Fatalf("%d requests went to the agent after Ctrl+C cut the command off", n)
	}
	waitFor(t, func() bool { _, err := os.Stat(out); return err == nil })
	joinJobs(t, sh)
	if sh.last.exitCode != 130 {
		t.Errorf("the line ended %d, want 130, as a line Ctrl+C interrupts", sh.last.exitCode)
	}
}

// The command of `cmd | ?? prompt` runs in a subshell, as the left side of
// a pipe does in bash: a cd or an assignment in it stays there, and exit
// or errexit end it, not the shell. It ran in the shell itself.
func TestPipeCommand_RunsInASubshell(t *testing.T) {
	tr := &scriptedTransport{scripts: []turnScript{
		{events: []agent.StreamEvent{textEvent("ok")}},
		{events: []agent.StreamEvent{textEvent("ok")}},
		{events: []agent.StreamEvent{textEvent("ok")}},
	}}
	sh, _, _ := newTurnShell(t, tr)
	orig, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(orig) })
	captureStdout(t, sh, func() {
		for _, line := range []string{"cd / && X=1 && echo hi | ?? summarize", "exit 3 | ?? summarize", "set -e", "false; echo x | ?? summarize"} {
			if err := sh.handleLine(context.Background(), line); err != nil {
				t.Errorf("%s: %v, want the shell to go on", line, err)
			}
		}
	})
	if cwd, _ := os.Getwd(); cwd != orig {
		t.Errorf("the shell is in %s, want %s: the command's cd moved it", cwd, orig)
	}
	var x strings.Builder
	if _, err := sh.executor.Execute(context.Background(), `printf %s "$X"`, &x, nil); err != nil || x.String() != "" {
		t.Errorf("X = %q in the shell, want it unset: the command's assignment stays in it", x.String())
	}
	if n := tr.requestCount(); n != 3 {
		t.Errorf("%d requests, want one for each ?? line", n)
	}
}

// What the command writes to stderr is not the agent's: it shows before
// the [agent: pipe] label, not on its row.
func TestPipeCommand_StderrComesBeforeTheLabel(t *testing.T) {
	tr := &scriptedTransport{scripts: []turnScript{{events: []agent.StreamEvent{textEvent("ok")}}}}
	sh, _, _ := newTurnShell(t, tr)
	out := captureStdout(t, sh, func() {
		sh.stderr = sh.stdout
		defer func() { sh.stderr = nil }()
		_ = sh.handleLine(context.Background(), "echo oops >&2; echo x | ?? summarize")
	})
	oops, label := strings.Index(out, "oops"), strings.Index(out, "[agent: pipe]")
	if oops < 0 || label < 0 || oops > label {
		t.Errorf("printed %q, want oops before the label", out)
	}
}
