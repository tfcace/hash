package editor

import (
	"bytes"
	"strings"
	"testing"
)

// hiding answers like tree, its menus leaving out hidden.
func hiding(tree func(string, int) []Completion, hidden CompletionHidden) func(string, int) CompletionOutcome {
	return func(line string, pos int) CompletionOutcome {
		return itemsOnly(tree)(line, pos).Hiding(hidden)
	}
}

// A lone item of a menu that left matches out is not the only match: Tab
// opens a menu of it, saying what was left out, rather than putting it in.
func TestCompletionHidden_LoneItemOpensAMenu(t *testing.T) {
	for hidden, note := range map[CompletionHidden]string{
		{Withheld: 1}:              "1 match can't be typed safely here",
		{Withheld: 2}:              "2 matches can't be typed safely here",
		{Uncounted: true}:          "more not shown, type more to narrow",
		{More: 12}:                 "12 more not shown, type more to narrow",
		{More: 4, Uncounted: true}: "more not shown, type more to narrow",
		{Withheld: 3, More: 4}:     "3 matches can't be typed safely here; 4 more not shown, type more to narrow",
	} {
		ed := newKeyEditor(t, Config{CompleteOutcomeFunc: hiding(fakeTree("README.md"), hidden)}, "cat ")
		press(t, ed, keyTab())
		if got := ed.state.Buffer.Content(); got != "cat " {
			t.Errorf("%+v: buffer = %q, want the lone item left in a menu", hidden, got)
		}
		if ed.menu == nil || len(ed.menu.items) != 1 || ed.menu.hidden.note() != note {
			t.Fatalf("%+v: menu %+v, want README.md noting %q", hidden, ed.menu, note)
		}
		press(t, ed, Key{Special: KeyEnter})
		if got := ed.state.Buffer.Content(); got != "cat README.md" {
			t.Errorf("%+v: Enter gives %q, want the item accepted", hidden, got)
		}
	}
}

// Drilling into a directory whose lone child came with matches left out
// stops there: the child is not the directory's only entry.
func TestCompletionHidden_NoAutoDrillPastALoneChild(t *testing.T) {
	tree := fakeTree("src/", "lib/", "src/b/", "src/b/x.go", "src/b/y.go")
	ed := newKeyEditor(t, Config{CompleteOutcomeFunc: func(line string, pos int) CompletionOutcome {
		out := itemsOnly(tree)(line, pos)
		if strings.HasSuffix(line[:pos], " src/") {
			out = out.Hiding(CompletionHidden{Withheld: 1})
		}
		return out
	}}, "cat ")
	press(t, ed, keyTab(), Key{Special: KeyRight})
	if got := ed.state.Buffer.Content(); got != "cat src/" {
		t.Fatalf("buffer = %q, want the drill to stop at src/", got)
	}
	if ed.menu == nil || len(ed.menu.items) != 1 || ed.menu.hidden.Withheld != 1 {
		t.Fatalf("menu %+v, want src/b/ with one withheld", ed.menu)
	}
}

// The note shows on a row under the menu's items.
func TestCompletionHidden_NoteShowsUnderTheMenu(t *testing.T) {
	var out bytes.Buffer
	ed := New(Config{CompleteOutcomeFunc: hiding(fakeTree("alpha", "alps"), CompletionHidden{Withheld: 2})}, strings.NewReader(""), &out)
	ed.SetInitialText("cat al")
	press(t, ed, keyTab())
	out.Reset()
	ed.render()
	if !strings.Contains(out.String(), "2 matches can't be typed safely here") {
		t.Errorf("render = %q, want the note", out.String())
	}
}
