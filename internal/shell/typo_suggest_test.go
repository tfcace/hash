package shell

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/executor"
	"github.com/tfcace/hash/internal/learning"
)

const pathspecStderr = "error: pathspec 'msater' did not match any file(s) known to git"

func TestGitDidYouMean_SuggestsClosestBranch(t *testing.T) {
	branches := func() []string { return []string{"develop", "master", "feat/demo"} }
	got := gitDidYouMean("git checkout msater", pathspecStderr, branches)
	if got != "git checkout master" {
		t.Errorf("gitDidYouMean() = %q, want %q", got, "git checkout master")
	}
}

func TestGitDidYouMean_NoCloseBranch(t *testing.T) {
	branches := func() []string { return []string{"develop", "release/1.0"} }
	if got := gitDidYouMean("git checkout msater", pathspecStderr, branches); got != "" {
		t.Errorf("gitDidYouMean() = %q, want empty (no close branch)", got)
	}
}

func TestGitDidYouMean_NonGitCommand(t *testing.T) {
	branches := func() []string { return []string{"master"} }
	if got := gitDidYouMean("jj checkout msater", pathspecStderr, branches); got != "" {
		t.Errorf("gitDidYouMean() = %q, want empty (not a git command)", got)
	}
}

func TestGitDidYouMean_TypoTokenNotInCommand(t *testing.T) {
	// Alias expansion can make stderr name a token the typed command lacks;
	// replacing nothing must yield no suggestion rather than a wrong one.
	branches := func() []string { return []string{"master"} }
	if got := gitDidYouMean("git co msater-extra", pathspecStderr, branches); got != "" {
		t.Errorf("gitDidYouMean() = %q, want empty (token absent)", got)
	}
}

func TestGitDidYouMean_ListerNotCalledWithoutPathspecError(t *testing.T) {
	branches := func() []string {
		t.Fatal("branch lister must not run for unrelated errors")
		return nil
	}
	if got := gitDidYouMean("git checkout msater", "fatal: not a git repository", branches); got != "" {
		t.Errorf("gitDidYouMean() = %q, want empty", got)
	}
}

func TestShell_DidYouMeanPreferredOverLearnedReplay(t *testing.T) {
	store := newTestFixStore(t)
	pattern := learning.ExtractPattern("git checkout msater", pathspecStderr, 1)
	// A learned literal replay exists for this pattern, from another context.
	if err := store.RecordFix(pattern, "git checkout -b feat/demo", true); err != nil {
		t.Fatalf("RecordFix() error = %v", err)
	}

	var banner bytes.Buffer
	s := &Shell{
		fixes:        newFixTracker(store),
		errors:       &ErrorHandler{out: &banner},
		branchLister: func() []string { return []string{"master", "develop"} },
	}

	cap1 := newStderrCapture(io.Discard)
	_, _ = cap1.Write([]byte(pathspecStderr))
	s.handleExecutionResult("git checkout msater", &executor.Result{ExitCode: 1}, nil, cap1)

	out := banner.String()
	if !strings.Contains(out, "git checkout master") {
		t.Fatalf("banner should suggest the close branch, got: %q", out)
	}
	if strings.Contains(out, "feat/demo") {
		t.Errorf("banner must not replay the contextual learned fix, got: %q", out)
	}
	if text := s.promptGhost(); text != "git checkout master" {
		t.Errorf("promptGhost() = %q, want the did-you-mean correction", text)
	}
}

func TestShell_LearnedFixStillShownWithoutCloseBranch(t *testing.T) {
	store := newTestFixStore(t)
	pattern := learning.ExtractPattern("git checkout msater", pathspecStderr, 1)
	if err := store.RecordFix(pattern, "git fetch --all", true); err != nil {
		t.Fatalf("RecordFix() error = %v", err)
	}

	var banner bytes.Buffer
	s := &Shell{
		fixes:        newFixTracker(store),
		errors:       &ErrorHandler{out: &banner},
		branchLister: func() []string { return []string{"develop"} },
	}

	cap1 := newStderrCapture(io.Discard)
	_, _ = cap1.Write([]byte(pathspecStderr))
	s.handleExecutionResult("git checkout msater", &executor.Result{ExitCode: 1}, nil, cap1)

	if !strings.Contains(banner.String(), "git fetch --all") {
		t.Errorf("learned fix should still show when no branch is close, got: %q", banner.String())
	}
}
