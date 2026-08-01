package editor

import (
	"io"
	"testing"
)

func TestCorrectionGhostRightFillsWithoutSubmit(t *testing.T) {
	ed := New(Config{CorrectionCandidates: []string{"git status"}}, nil, io.Discard)
	if ed.ghost.Source != GhostSourceCorrection {
		t.Fatalf("source = %v", ed.ghost.Source)
	}
	if !ed.handleGhostTextKey(Key{Special: KeyRight}) {
		t.Fatal("Right was not handled")
	}
	if got := ed.state.Buffer.Content(); got != "git status" {
		t.Fatalf("buffer = %q", got)
	}
}

func TestCorrectionGhostEnterDismissesWithoutFilling(t *testing.T) {
	ed := New(Config{CorrectionCandidates: []string{"git status"}}, nil, io.Discard)
	if !ed.handleGhostTextKey(Key{Special: KeyEnter}) {
		t.Fatal("Enter was not consumed")
	}
	if got := ed.state.Buffer.Content(); got != "" {
		t.Fatalf("buffer = %q", got)
	}
}

func TestCorrectionChooserEnterFillsWithoutSubmitting(t *testing.T) {
	ed := New(Config{CorrectionCandidates: []string{"git status", "git stash"}}, nil, io.Discard)
	if !ed.correctionMode || !ed.completionActive {
		t.Fatal("correction chooser is not active")
	}
	if !ed.handleCompletionKey(Key{Special: KeyEnter}) {
		t.Fatal("Enter was not handled")
	}
	if got := ed.state.Buffer.Content(); got != "git status" {
		t.Fatalf("buffer = %q", got)
	}
	if ed.completionActive {
		t.Fatal("chooser remained active")
	}
}

func TestCorrectionChooserSupportsCtrlNAndCtrlP(t *testing.T) {
	ed := New(Config{CorrectionCandidates: []string{"git status", "git stash"}}, nil, io.Discard)
	if !ed.handleCompletionKey(Key{Rune: 'n', Ctrl: true}) || ed.completionIndex != 1 {
		t.Fatalf("Ctrl-N did not select next candidate: index=%d", ed.completionIndex)
	}
	if !ed.handleCompletionKey(Key{Rune: 'p', Ctrl: true}) || ed.completionIndex != 0 {
		t.Fatalf("Ctrl-P did not select previous candidate: index=%d", ed.completionIndex)
	}
}

func TestOwnedGhostTextPreservesLearnedFixSource(t *testing.T) {
	ed := New(Config{}, nil, io.Discard)
	ed.SetGhostTextOwned("git status", GhostSourceLearnedFix)
	if ed.ghost.Source != GhostSourceLearnedFix {
		t.Fatalf("source=%v", ed.ghost.Source)
	}
}
