package shell

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// !! asks before it opens the editor on an issue, whatever the last line
// did: it opened $EDITOR at once after a failure, a surprise for a user
// who typed it for bash's history expansion. No keeps the editor shut.
func TestIssueShortcut_AsksAfterAFailure(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "opened")
	editor := filepath.Join(t.TempDir(), "editor")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\n: >"+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", editor)
	t.Setenv("EDITOR", editor)
	sh, typing := terminalShell(t)
	sh.last = commandOutcome{command: "false", exitCode: 1}
	ctx := questionCtx(t)
	done := make(chan struct{})
	out := captureStdout(t, sh, func() {
		go func() { defer close(done); sh.handleIssueShortcut(ctx) }()
		time.Sleep(30 * time.Millisecond)
		typing.WriteString("n\r")
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("!! did not return after n")
		}
	})
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("!! opened the editor without asking (printed %q)", out)
	}
	if !strings.Contains(out, "[y/N]") {
		t.Errorf("printed %q, want a question", out)
	}
}

// !! reports on the line before it; it is no command of the user's: it is
// not saved in history, the predictor never learns it as a step of a
// sequence, and it does not resolve the failure before it, whose fix the
// learning loop still waits for. It was recorded as a command.
func TestIssueShortcut_IsNotACommand(t *testing.T) {
	sh := newShellForAgentCommand(t)
	if sh.history == nil || sh.predictor == nil || !sh.fixes.active() {
		t.Fatal("the shell has no history, predictor or learning loop to check")
	}
	cwd, _ := os.Getwd()
	run := func(line string) {
		captureStdout(t, sh, func() { _ = sh.handleLine(context.Background(), line) })
	}
	for range 3 {
		run("!!") // no terminal: declined
		run("echo next")
	}
	run("false")
	run("!!")
	if p := sh.fixes.pending; p == nil || p.command != "false" {
		t.Errorf("after !! the learning loop waits for a fix of %+v, want false", p)
	}
	recent, err := sh.history.GetRecent(20)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range recent {
		if c.Command == "!!" {
			t.Errorf("history has !!: %+v", recent)
			break
		}
	}
	if next := sh.predictor.PredictCommand("!!", cwd); next != "" {
		t.Errorf("the predictor follows !! with %q", next)
	}
}

// !! is a line with a status of its own (the prompt, $? and OSC 133 D
// report it) but no command: the line a bare ??, a second !! and the
// predictor follow is still the one before it. A declined !! replaced it,
// so a bare ?? had no failure left to explain and a second !! asked about
// the first.
func TestIssueShortcut_KeepsTheLineBeforeIt(t *testing.T) {
	sh := newShellForAgentCommand(t)
	run := func(line string) {
		captureStdout(t, sh, func() { _ = sh.handleLine(context.Background(), line) })
	}
	run("false")
	run("!!") // no terminal: declined
	if le := sh.last.failedCommand(); le == nil || le.Command != "false" {
		t.Errorf("after a declined !!, a bare ?? explains %+v, want the failed false", le)
	}
	if q := issueQuestion(sh.last); q != "Open an issue about the last command (exit 1)? [y/N] " {
		t.Errorf("a second !! asks %q, want it about the failed false", q)
	}
	if got := dollarQuestion(t, sh); got != "0" {
		t.Errorf("$? after !! = %s, want the !! line's 0", got)
	}
}

// Ctrl+C at !!'s question interrupts the line, which ends with 130 as an
// interrupted command does; n, Esc or Enter decline it, which is no failure.
func TestIssueShortcut_CtrlCAtTheQuestionIs130(t *testing.T) {
	t.Parallel()
	for keys, want := range map[string]int{"\x03": 130, "\x1b": 0, "n\r": 0} {
		sh, typing := terminalShell(t)
		sh.last = commandOutcome{command: "false", exitCode: 1}
		sh.status = sh.last.status()
		ctx := questionCtx(t)
		done := make(chan struct{})
		captureStdout(t, sh, func() {
			go func() { defer close(done); sh.handleIssueShortcut(ctx) }()
			time.Sleep(30 * time.Millisecond)
			typing.WriteString(keys)
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatalf("%q: !! did not return", keys)
			}
		})
		if sh.status.exitCode != want {
			t.Errorf("%q at the question: status %d, want %d", keys, sh.status.exitCode, want)
		}
	}
}
