package shell

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

// newShellForTypo builds a real shell whose only known command is git, with
// the error banner captured, so a mistyped "gti" is corrected deterministically.
func newShellForTypo(t *testing.T, out io.Writer) *Shell {
	t.Helper()
	sh := newShellForAgentCommand(t)
	sh.errors = &ErrorHandler{out: out}
	suggestor := &CommandSuggestor{pathCache: []string{"git"}}
	suggestor.pathCacheReady.Store(true)
	sh.suggestor = suggestor
	return sh
}

// A did-you-mean correction for a mistyped command name becomes the ghost
// text at the next prompt, with the same accept affordance as a learned fix,
// so the user does not retype the line.
func TestCommandNotFound_SuggestionBecomesGhost(t *testing.T) {
	var out bytes.Buffer
	sh := newShellForTypo(t, &out)

	_ = sh.executeRegularCommand(context.Background(), "gti status")

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

	_ = sh.executeRegularCommand(context.Background(), "echo x&&gti status")

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

	_ = sh.executeRegularCommand(context.Background(), "gti status")

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
	sh.suggestor = &CommandSuggestor{}
	sh.suggestor.pathCacheReady.Store(true)

	_ = sh.executeRegularCommand(context.Background(), "xyzzy")

	if strings.Contains(out.String(), "→ to accept") {
		t.Errorf("banner should not offer an accept key with no suggestion, got:\n%s", out.String())
	}
}
