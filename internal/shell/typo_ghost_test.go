package shell

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// newShellForTypo builds a real shell whose only known command is git, with
// the error banner captured, so a mistyped "gti" is corrected deterministically.
func newShellForTypo(t *testing.T, out io.Writer) *Shell {
	t.Helper()
	sh := newShellForAgentCommand(t)
	sh.errors = &ErrorHandler{out: out}
	sh.suggestor = fixedSuggestor("git")
	return sh
}

// A did-you-mean correction for a mistyped command name becomes the ghost
// text at the next prompt, with the same accept affordance as a learned fix,
// so the user does not retype the line.
func TestCommandNotFound_SuggestionBecomesGhost(t *testing.T) {
	var out bytes.Buffer
	sh := newShellForTypo(t, &out)

	_, _ = sh.runShellLine(context.Background(), "gti status")

	if got := sh.promptGhost(); got != "git status" {
		t.Errorf("promptGhost() = %q, want the corrected line", got)
	}
	if !strings.Contains(out.String(), "→ to accept") {
		t.Errorf("banner should teach the accept key, got:\n%s", out.String())
	}
}

// The correction applies when the mistyped name touches a shell operator.
func TestCommandNotFound_CorrectionCrossesShellOperators(t *testing.T) {
	var out bytes.Buffer
	sh := newShellForTypo(t, &out)

	_, _ = sh.runShellLine(context.Background(), "echo x&&gti status")

	if got := sh.promptGhost(); got != "echo x&&git status" {
		t.Errorf("promptGhost() = %q, want the corrected line", got)
	}
}

// The banner promises the accept key only when a ghost will actually be
// offered: without a learning loop there is nothing to accept.
func TestCommandNotFound_NoAcceptHintWithoutALearningLoop(t *testing.T) {
	var out bytes.Buffer
	sh := newShellForTypo(t, &out)
	sh.fixes = newFixTracker(nil)

	_, _ = sh.runShellLine(context.Background(), "gti status")

	if !strings.Contains(out.String(), "did you mean") {
		t.Errorf("banner should still name the correction, got:\n%s", out.String())
	}
	if strings.Contains(out.String(), "→ to accept") {
		t.Errorf("banner should not offer an accept key it cannot honor, got:\n%s", out.String())
	}
}

// Without a suggestion there is nothing to accept, so the banner must not
// promise it.
func TestCommandNotFound_NoSuggestionNoAcceptHint(t *testing.T) {
	var out bytes.Buffer
	sh := newShellForTypo(t, &out)
	sh.suggestor = fixedSuggestor()

	_, _ = sh.runShellLine(context.Background(), "xyzzy")

	if strings.Contains(out.String(), "→ to accept") {
		t.Errorf("banner should not offer an accept key with no suggestion, got:\n%s", out.String())
	}
}

// Two mistyped names on one line: the offer corrects both, not just the
// one each refusal happened to see last.
func TestCommandNotFound_EveryTypoOnTheLineIsCorrected(t *testing.T) {
	var out bytes.Buffer
	sh := newShellForTypo(t, &out)

	_, _ = sh.runShellLine(context.Background(), "gti status; gti log")

	if got := sh.promptGhost(); got != "git status; git log" {
		t.Errorf("promptGhost() = %q, want both typos corrected", got)
	}
	if n := strings.Count(out.String(), "→ to accept"); n != 1 {
		t.Errorf("accept key taught %d times, want once for the line:\n%s", n, out.String())
	}
}

// A background job's refusal is reported from its own goroutine while the
// user's next line runs: drawing it must not touch the line's state.
func TestCommandNotFound_BackgroundRefusalDoesNotRaceTheNextLine(t *testing.T) {
	var out lockedBuffer
	sh := newShellForTypo(t, &out)

	_, _ = sh.runShellLine(context.Background(), "(sleep 0.02; gti) &")
	for range 20 {
		_, _ = sh.runShellLine(context.Background(), "true")
		time.Sleep(2 * time.Millisecond)
	}
	_, _ = sh.runShellLine(context.Background(), "wait")
}

// A correction that holds the mistyped name (wwkk -> "wwkk;") is put in
// once per occurrence: the corrected text is not searched again, so the
// correction ends instead of growing the line forever.
func TestCorrectTypo_CorrectionHoldingTheTypoEnds(t *testing.T) {
	done := make(chan string, 1)
	go func() { done <- correctTypo("wwkk; wwkk x", "wwkk", []string{"wwkk;"}) }()
	select {
	case got := <-done:
		if want := "wwkk;; wwkk; x"; got != want {
			t.Errorf("correctTypo() = %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("correctTypo did not return: it keeps correcting its own correction")
	}
}
