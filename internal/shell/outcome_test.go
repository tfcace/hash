package shell

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/prompt"
)

// A ?? that fails says nothing about the shell command before it: the next
// bare ?? must not tell the agent that command "failed (exit 1)".
func TestOutcome_FailedAgentTurnDoesNotBlameThePreviousCommand(t *testing.T) {
	t.Parallel()
	tr := &scriptedTransport{scripts: []turnScript{
		{err: errors.New("agent exploded")}, // not retryable: no confirmation
		{events: []agent.StreamEvent{textEvent("Nothing failed.")}},
	}}
	sh, _, _ := newTurnShell(t, tr)
	sh.errors = &ErrorHandler{out: &lockedBuffer{}}
	if _, err := sh.runShellLine(context.Background(), "true"); err != nil {
		t.Fatal(err)
	}

	if err := ask(t, sh, "?? break"); err != nil {
		t.Fatal(err)
	}
	if err := ask(t, sh, "??"); err != nil {
		t.Fatal(err)
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(tr.requests))
	}
	bare := tr.requests[1]
	if bare.Context.LastError != "" || strings.Contains(bare.Prompt, "true") {
		t.Errorf("bare ?? blamed the earlier command: prompt %q, last error %q", bare.Prompt, bare.Context.LastError)
	}
}

// A hook is not the user's line: a missing command in a chpwd hook must not
// turn a successful cd into a failure in the prompt and OSC 133 D.
func TestOutcome_ChpwdHookRefusalDoesNotFailTheCd(t *testing.T) {
	sh := newShellForAgentCommand(t)
	sh.errors = &ErrorHandler{out: &lockedBuffer{}}
	sh.config.Shell.Hooks.Chpwd = []string{"hash-test-no-such-hook-command"}
	dir := t.TempDir()
	orig, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(orig) })

	if _, err := sh.runShellLine(context.Background(), "cd "+dir); err != nil {
		t.Fatal(err)
	}
	sh.runChpwdHook(context.Background())

	if sh.last.exitCode != 0 {
		t.Errorf("exit code after a successful cd = %d, want 0", sh.last.exitCode)
	}
}

// $? after a line is the line's status, the one the prompt and OSC 133 D
// show, whatever a hook ran after it: a failing chpwd hook after a
// successful cd used to leave $? at 1.
func TestOutcome_ChpwdHookStatusStaysOutOfDollarQuestion(t *testing.T) {
	sh := newShellForAgentCommand(t)
	sh.config.Shell.Hooks.Chpwd = []string{"false"}
	orig, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(orig) })

	captureStdout(t, sh, func() { sh.handleLine(context.Background(), "cd "+t.TempDir()) })
	if got := dollarQuestion(t, sh); got != "0" || sh.last.exitCode != 0 {
		t.Errorf("$? = %s, line exit = %d; want 0 for a successful cd", got, sh.last.exitCode)
	}

	captureStdout(t, sh, func() { sh.handleLine(context.Background(), "cd "+t.TempDir()+" && false") })
	if got := dollarQuestion(t, sh); got != "1" || sh.last.exitCode != 1 {
		t.Errorf("$? = %s, line exit = %d; want 1 for a failed line", got, sh.last.exitCode)
	}
}

// A builtin is a line the user ran: the command a bare ?? and the predictor
// read must be it, not the line before.
func TestOutcome_BuiltinIsTheLastCommand(t *testing.T) {
	sh := newShellForAgentCommand(t)
	dir := t.TempDir()
	orig, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(orig) })

	if _, err := sh.runShellLine(context.Background(), "false"); err != nil {
		t.Fatal(err)
	}
	line := "cd " + dir
	if _, err := sh.runShellLine(context.Background(), line); err != nil {
		t.Fatal(err)
	}
	if sh.last.command != line {
		t.Errorf("last command = %q, want the builtin %q", sh.last.command, line)
	}
}

// An agent command that ran is accepted even when history could not record
// it: whether it ran is not whether it got an id.
func TestOutcome_AgentCommandRunIsAcceptedWithoutAHistoryID(t *testing.T) {
	sh := newShellForAgentCommand(t)
	db, err := sql.Open("sqlite3", sh.historyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER no_commands BEFORE INSERT ON commands BEGIN SELECT RAISE(ABORT, 'disk full'); END`); err != nil {
		t.Fatal(err)
	}

	sh.handleAgentConfirmAction(startTurn(t, sh), ConfirmRun, ConfirmTypeCommand,
		agent.Response{Type: agent.ResponseTypeCommand, Command: "true"}, agentTurn{prompt: "run true", response: "true"})

	turns, err := sh.history.GetAgentInteractions("", 10)
	if err != nil || len(turns) != 1 {
		t.Fatalf("GetAgentInteractions() = %v, %v; want one recorded turn", turns, err)
	}
	if !turns[0].Accepted {
		t.Errorf("the command ran but the turn was recorded as not accepted: %+v", turns[0])
	}
}

// After a builtin such as cd, the next prompt's info bar was drawn twice:
// once by the builtin path and once by the REPL loop.
func TestHandleLine_DrawsTheInfoBarOnceAfterABuiltin(t *testing.T) {
	starship := filepath.Join(t.TempDir(), "starship")
	if err := os.WriteFile(starship, []byte("#!/bin/sh\nprintf 'INFO-BAR\\n> '\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sh := newShellForAgentCommand(t)
	sh.prompt = prompt.New(prompt.Config{Mode: "starship", StarshipPath: starship})
	dir := t.TempDir()
	orig, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(orig) })

	for _, line := range []string{"cd " + dir, "true", "# just a comment"} {
		out := captureStdout(t, sh, func() { // the line, then the next prompt's head
			_ = sh.handleLine(context.Background(), line)
			sh.drawPromptHead(nil)
		})
		if got := strings.Count(out, "INFO-BAR"); got != 1 {
			t.Errorf("%q drew the info bar %d times, want once: %q", line, got, out)
		}
	}
}

// A !! line is the issue builtin for the last failure: it is the line the
// user ran, so its status is the one the next prompt and OSC 133 D report,
// as `issue --last` typed out is. It used to leave the failure's status. It
// is no command, so the last command stays the failure.
func TestOutcome_IssueShortcutIsTheLastLine(t *testing.T) {
	t.Setenv("VISUAL", "true")
	t.Setenv("EDITOR", "true") // leaves the issue unchanged: aborted, no error
	sh, _, _ := newTurnShell(t, &scriptedTransport{})
	sh.last = commandOutcome{command: "false", exitCode: 1}
	sh.status = sh.last.status()

	sh.handleLine(context.Background(), "!!")
	if sh.status.exitCode != 0 || sh.last.command != "false" {
		t.Errorf("status %d, last command %q; want the !! line's 0 and false still the last command", sh.status.exitCode, sh.last.command)
	}
}

// dollarQuestion is what $? expands to in the shell's interpreter now.
func dollarQuestion(t *testing.T, sh *Shell) string {
	t.Helper()
	var out strings.Builder
	if _, err := sh.executor.Execute(context.Background(), "echo $?", &out, nil); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out.String())
}

// $? is the status of the line before, whatever ran it: a ?? line that
// failed, or a builtin hash runs itself (cd), ends with 1 in the prompt and
// OSC 133 D, so $? must say 1 too, not the status of the last line the
// interpreter happened to run.
func TestOutcome_DollarQuestionIsTheLinesStatus(t *testing.T) {
	tr := &scriptedTransport{scripts: []turnScript{{err: errFlaky}}}
	sh, _, _ := newTurnShell(t, tr)

	captureStdout(t, sh, func() { sh.handleLine(context.Background(), "?? anything") })
	if sh.last.exitCode != 1 {
		t.Fatalf("?? line exit = %d, want 1", sh.last.exitCode)
	}
	if got := dollarQuestion(t, sh); got != "1" {
		t.Errorf("$? after a failed ?? line = %s, want 1", got)
	}

	captureStdout(t, sh, func() {
		sh.handleLine(context.Background(), "true")
		sh.handleLine(context.Background(), "cd /nonexistent/dir")
	})
	if got := dollarQuestion(t, sh); got != "1" {
		t.Errorf("$? after a failed cd = %s, want 1", got)
	}
}

// errTrapRuns counts the runs of an ERR trap that appends to a file, over the
// lines run after it is set.
func errTrapRuns(t *testing.T, sh *Shell, lines ...string) int {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "err-trap")
	captureStdout(t, sh, func() {
		sh.handleLine(context.Background(), "trap 'echo ran >> "+marker+"' ERR")
		for _, line := range lines {
			sh.handleLine(context.Background(), line)
		}
	})
	b, _ := os.ReadFile(marker)
	return strings.Count(string(b), "ran")
}

// Setting $? for a line the interpreter never ran (a failed ?? request)
// must not run the user's ERR trap: no command failed in the shell. It used
// to run because the status was set by running `(exit N)`.
func TestOutcome_ErrTrapDoesNotFireForAFailedAgentLine(t *testing.T) {
	tr := &scriptedTransport{scripts: []turnScript{{err: errors.New("agent exploded")}}}
	sh, _, _ := newTurnShell(t, tr)
	if n := errTrapRuns(t, sh, "?? anything"); n != 0 {
		t.Errorf("ERR trap ran %d times for a failed ?? line, want 0", n)
	}
	if got := dollarQuestion(t, sh); got != "1" {
		t.Errorf("$? = %s, want 1", got)
	}
}

// A failed line runs the ERR trap once, as the interpreter runs it; setting
// $? afterwards must not run it again.
func TestOutcome_ErrTrapFiresOnceForAFailedLine(t *testing.T) {
	sh, _, _ := newTurnShell(t, &scriptedTransport{})
	if n := errTrapRuns(t, sh, "false"); n != 1 {
		t.Errorf("ERR trap ran %d times for one failed line, want 1", n)
	}
}
