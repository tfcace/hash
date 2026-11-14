package editor

import (
	"strings"
	"testing"
)

// pendingThenLanded is a completer whose source is loading until landed is
// set, then lists one match (which a fresh Tab puts in place).
func pendingThenLanded(landed *bool) func(string, int) CompletionOutcome {
	return func(line string, pos int) CompletionOutcome {
		if !*landed {
			return EmptyOutcome(CompletionPending)
		}
		return wordMenu(line, pos, Completion{Text: "redis"})
	}
}

// Dismissing the "fetching" notice drops the pending completion with it: a
// source landing later must not edit a line the user already moved on from,
// even when the line and cursor are back where Tab was pressed.
func TestCompletionStatus_DismissedFetchingNeverLands(t *testing.T) {
	dismissals := map[string][]Key{
		"Esc":        {{Special: KeyEscape}},
		"Ctrl+L":     {{Rune: 'l', Ctrl: true}},
		"Left+Right": {{Special: KeyLeft}, {Special: KeyRight}},
	}
	for name, keys := range dismissals {
		landed := false
		ed := newKeyEditor(t, Config{CompleteOutcomeFunc: pendingThenLanded(&landed)}, "docker rm ")
		press(t, ed, keyTab())
		press(t, ed, keys...)
		if name == "Esc" {
			press(t, ed, runes("A")...) // Esc left insert mode; A appends again
		}
		landed = true
		ed.handleCompletionReady()
		if got := ed.state.Buffer.Content(); got != "docker rm " {
			t.Errorf("%s: buffer = %q after the source landed, want the line left alone", name, got)
		}
	}
}

// slowDir answers from tree, except that listings under dir are still
// loading (Pending) or ran out of time (TimedOut).
func slowDir(tree func(string, int) []Completion, dir string, st CompletionStatus) func(string, int) CompletionOutcome {
	return func(line string, pos int) CompletionOutcome {
		if strings.HasSuffix(line[:pos], dir) {
			return EmptyOutcome(st)
		}
		return itemsOnly(tree)(line, pos)
	}
}

// Drilling into a directory whose listing has not arrived says so (and
// retries when it lands) instead of treating it as an empty directory.
func TestCompletionFeedback_DrillIntoPendingListing(t *testing.T) {
	for st, want := range map[CompletionStatus]feedbackKind{
		CompletionPending:  feedbackFetching,
		CompletionTimedOut: feedbackTimedOut,
	} {
		tree := fakeTree("src/", "lib/", "src/a.go", "src/b.go")
		ed := newKeyEditor(t, Config{CompleteOutcomeFunc: slowDir(tree, "src/", st)}, "cat ")
		press(t, ed, keyTab(), Key{Special: KeyRight})
		if got := ed.state.Buffer.Content(); got != "cat src/" {
			t.Fatalf("buffer = %q, want the drilled word", got)
		}
		if ed.menu != nil || ed.feedback.kind != want {
			t.Errorf("status %v: menu=%v feedback=%v, want no menu and feedback %v", st, ed.menu != nil, ed.feedback.kind, want)
		}
	}
}

// Drilling back up to a listing that is still loading says so too.
func TestCompletionFeedback_DrillUpToPendingListing(t *testing.T) {
	tree := fakeTree("src/", "lib/", "src/a.go", "src/b.go")
	loading := false
	ed := newKeyEditor(t, Config{CompleteOutcomeFunc: func(line string, pos int) CompletionOutcome {
		if loading {
			return EmptyOutcome(CompletionPending)
		}
		return itemsOnly(tree)(line, pos)
	}}, "cat ")
	press(t, ed, keyTab(), Key{Special: KeyRight})
	loading = true
	press(t, ed, keyBackspace())
	if ed.menu != nil || ed.feedback.kind != feedbackFetching {
		t.Errorf("menu=%v feedback=%v, want no menu and fetching", ed.menu != nil, ed.feedback.kind)
	}
}

// The listing that was still loading opens by itself when it lands, without
// another key: the ready signal is not ignored after a drill.
func TestCompletionFeedback_DrilledListingOpensWhenReady(t *testing.T) {
	tree := fakeTree("src/", "lib/", "src/a.go", "src/b.go")
	loading := true
	ed := newKeyEditor(t, Config{CompleteOutcomeFunc: func(line string, pos int) CompletionOutcome {
		if loading && strings.HasSuffix(line[:pos], "src/") {
			return EmptyOutcome(CompletionPending)
		}
		return itemsOnly(tree)(line, pos)
	}}, "cat ")
	press(t, ed, keyTab(), Key{Special: KeyRight})
	loading = false
	ed.handleCompletionReady()
	if ed.menu == nil || len(ed.menu.items) != 2 || ed.menu.items[0].Text != "src/a.go" {
		t.Fatalf("menu after the listing landed = %+v, want src/a.go, src/b.go", ed.menu)
	}
}
