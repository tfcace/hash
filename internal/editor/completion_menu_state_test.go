package editor

import (
	"strings"
	"testing"
)

// fakeTree completes the word before the cursor against a fixed set of paths,
// listing only the direct children of the word's directory, the way the
// filesystem completer does.
func fakeTree(paths ...string) func(line string, pos int) []Completion {
	return func(line string, pos int) []Completion {
		word := line[:pos]
		if i := strings.LastIndexAny(word, " \n"); i >= 0 {
			word = word[i+1:]
		}
		dir := word[:strings.LastIndex(word, "/")+1]
		var out []Completion
		for _, p := range paths {
			if !strings.HasPrefix(p, word) {
				continue
			}
			rest := strings.TrimPrefix(p, dir)
			if rest == "" {
				continue
			}
			if i := strings.Index(rest, "/"); i >= 0 && i != len(rest)-1 {
				continue
			}
			out = append(out, Completion{Text: p})
		}
		return out
	}
}

func treeEditor(t *testing.T, line string) *Editor {
	t.Helper()
	return newKeyEditor(t, Config{
		CompleteOutcomeFunc: itemsOnly(fakeTree("src/", "src/a.go", "src/ab.go", "src/b.go", "lib/", "qa", "qb")),
	}, line)
}

// After a narrowing char found nothing, the menu is closed for good: a later,
// unrelated menu must not inherit the old drill stack and delete text from
// its stale column.
func TestCompletionMenu_EmptyRefineDropsDrillState(t *testing.T) {
	ed := treeEditor(t, "cat ")
	press(t, ed, keyTab(), Key{Special: KeyRight}) // open, drill into src/
	if got := ed.state.Buffer.Content(); got != "cat src/" {
		t.Fatalf("after drill buffer = %q", got)
	}

	press(t, ed, seq(runes("z"), one(keyBackspace()), runes(" q"), one(keyTab()), one(keyBackspace()))...)

	if got := ed.state.Buffer.Content(); got != "cat src/ " {
		t.Fatalf("buffer = %q, want only the q removed", got)
	}
}

// Backspace after narrowing a drilled menu removes the narrowing character;
// it drills up only once the word is back to the drilled directory.
func TestCompletionMenu_BackspaceUnNarrowsBeforeDrillingUp(t *testing.T) {
	ed := treeEditor(t, "cat ")
	press(t, ed, keyTab(), Key{Special: KeyRight}, Key{Rune: 'a'})
	if got := ed.state.Buffer.Content(); got != "cat src/a" {
		t.Fatalf("after narrowing buffer = %q", got)
	}

	press(t, ed, keyBackspace())
	if got := ed.state.Buffer.Content(); got != "cat src/" {
		t.Fatalf("first Backspace: buffer = %q, want %q", got, "cat src/")
	}
	if ed.menu == nil {
		t.Fatal("menu closed after un-narrowing")
	}

	press(t, ed, keyBackspace())
	if got := ed.state.Buffer.Content(); got != "cat " {
		t.Fatalf("second Backspace: buffer = %q, want the drill undone", got)
	}
}

// A completion pending on an empty line retries when its source lands, like
// on any other line.
func TestCompletionMenu_PendingOnEmptyLineRetries(t *testing.T) {
	landed := false
	ed := newKeyEditor(t, Config{
		CompleteOutcomeFunc: func(line string, pos int) CompletionOutcome {
			if !landed {
				return EmptyOutcome(CompletionPending)
			}
			return wordMenu(line, pos, Completion{Text: "one"}, Completion{Text: "two"})
		},
	}, "")
	press(t, ed, keyTab())

	landed = true
	ed.handleCompletionReady()

	if ed.menu == nil {
		t.Fatal("a pending completion on an empty line never retried")
	}
}

// Drilling up from a nested directory goes back one level, to the parent
// directory's listing, not all the way to the word the menu opened on.
func TestCompletionMenu_NestedDrillUpGoesBackOneLevel(t *testing.T) {
	ed := newKeyEditor(t, Config{
		CompleteOutcomeFunc: itemsOnly(fakeTree("src/", "lib/", "src/a.go", "src/b/", "src/b/x.go", "src/b/y.go")),
	}, "cat ")
	press(t, ed, keyTab(), Key{Special: KeyRight}, Key{Special: KeyDown}, Key{Special: KeyRight})
	if got := ed.state.Buffer.Content(); got != "cat src/b/" {
		t.Fatalf("after nested drill buffer = %q", got)
	}

	press(t, ed, keyBackspace())
	if got := ed.state.Buffer.Content(); got != "cat src/" {
		t.Fatalf("Backspace: buffer = %q, want back in src/", got)
	}
}

// The menu and the feedback under the line never show together: opening
// the menu ends the feedback, feedback with something to say closes the
// menu, and ending the feedback leaves the menu open.
func TestEditor_MenuOrFeedbackNeverBoth(t *testing.T) {
	ed := newKeyEditor(t, Config{}, "")
	ed.setFeedback(lineFeedback{kind: feedbackNoMatches})
	ed.openMenu(&completionMenu{items: []Completion{{Text: "a"}, {Text: "b"}}})
	if ed.feedback.kind != feedbackNone {
		t.Errorf("menu opened over feedback %v", ed.feedback.kind)
	}
	ed.setFeedback(lineFeedback{})
	if ed.menu == nil {
		t.Error("ending the feedback closed the menu")
	}
	ed.setFeedback(lineFeedback{kind: feedbackPasteCut})
	if ed.menu != nil {
		t.Error("feedback shown with the menu open")
	}
}

// A menu narrowed to one candidate: Tab accepts it (it goes in place and
// the menu closes), as a lone match does when Tab first lists the word,
// and as zsh and fish do. Tab cycled the one row, so Tab did nothing and
// only Enter accepted. Shift+Tab and the arrows still move in the menu.
func TestCompletionMenu_TabAcceptsTheOneCandidateLeft(t *testing.T) {
	for _, tc := range []struct {
		typed, want string
		menu        bool
	}{
		{"qa", "cat qa", false},
		{"s", "cat src/", false}, // a directory: the next Tab lists it
	} {
		ed := treeEditor(t, "cat ")
		press(t, ed, keyTab())
		press(t, ed, runes(tc.typed)...)
		if ed.menu == nil || len(ed.menu.items) != 1 {
			t.Fatalf("%q: menu %+v, want one candidate left", tc.typed, ed.menu)
		}
		press(t, ed, keyTab())
		if ed.menu != nil || ed.state.Buffer.Content() != tc.want {
			t.Errorf("%q then Tab: menu open %v, buffer %q; want %q, the menu closed", tc.typed, ed.menu != nil, ed.state.Buffer.Content(), tc.want)
		}
	}

	ed := treeEditor(t, "cat ")
	press(t, ed, append([]Key{keyTab()}, runes("qa")...)...)
	press(t, ed, Key{Special: KeyTab, Shift: true}, Key{Special: KeyDown})
	if ed.menu == nil {
		t.Error("Shift+Tab or Down on one candidate closed the menu")
	}
}
