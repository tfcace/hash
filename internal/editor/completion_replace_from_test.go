package editor

import (
	"strings"
	"testing"
)

// replacing is a completer that answers every request with items that
// replace the buffer from the byte where marker starts.
func replacing(marker string, items ...string) func(string, int) CompletionOutcome {
	return func(line string, pos int) CompletionOutcome {
		var menu []Completion
		for _, it := range items {
			menu = append(menu, Completion{Text: it})
		}
		return MenuOutcome(menu, strings.LastIndex(line[:pos], marker))
	}
}

// The completer says where the text its items replace starts, and the
// editor replaces from there: the word it completes need not be the
// editor's blank-split word. The editor used to replace from its own word
// start on the cursor's row, so `$((1 + $HO` became `$HOME` alone, and a
// quoted word begun on an earlier row was doubled.
func TestEditor_CompletionReplacesFromTheCompletersStart(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		complete   func(string, int) CompletionOutcome
		want       string
	}{
		{"arithmetic", "echo $((1 + $HO", replacing("$HO", "$HOME"), "echo $((1 + $HOME"},
		{"word begun on an earlier row", "ls \"my\ndi", replacing("\"my", "\"my\ndir/\""), "ls \"my\ndir/\""},
		{"menu, accepted", "echo $((1 + $HO", replacing("$HO", "$HOME", "$HOST"), "echo $((1 + $HOME"},
	} {
		ed := newKeyEditor(t, Config{CompleteOutcomeFunc: tc.complete}, tc.text)
		ed.state.Cursor.Pos = Position{ed.state.Buffer.LineCount() - 1, len(ed.state.Buffer.Line(ed.state.Buffer.LineCount() - 1))}
		press(t, ed, keyTab())
		if ed.menu != nil {
			press(t, ed, Key{Special: KeyEnter})
		}
		if got := ed.state.Buffer.Content(); got != tc.want {
			t.Errorf("%s: Tab on %q gives %q, want %q", tc.name, tc.text, got, tc.want)
		}
		if got := ed.cursorOffset(); got != len(tc.want) {
			t.Errorf("%s: cursor at %d, want the end (%d)", tc.name, got, len(tc.want))
		}
	}
}

// With a start past the cursor (a completer's bug) the editor takes the
// word at the cursor from the whole buffer: an unquoted newline ends a word
// like a blank.
func TestEditor_CompletionWordStartFromTheWholeBuffer(t *testing.T) {
	unset := func(item string) func(string, int) CompletionOutcome {
		return func(line string, pos int) CompletionOutcome {
			return MenuOutcome([]Completion{{Text: item}}, pos+1)
		}
	}
	for _, tc := range []struct{ text, item, want string }{
		{"echo a\nfo", "foo", "echo a\nfoo"},
		{"echo \"a\nb", "\"a\nbc\"", "echo \"a\nbc\""},
	} {
		ed := newKeyEditor(t, Config{CompleteOutcomeFunc: unset(tc.item)}, tc.text)
		ed.state.Cursor.Pos = Position{1, len(ed.state.Buffer.Line(1))}
		press(t, ed, keyTab())
		if got := ed.state.Buffer.Content(); got != tc.want {
			t.Errorf("Tab on %q gives %q, want %q", tc.text, got, tc.want)
		}
	}
}

// An outcome whose start is past the cursor (a completer's bug) replaces
// the word at the cursor, not from the start it gave.
func TestEditor_CompletionStartPastTheCursorReplacesTheWord(t *testing.T) {
	complete := func(line string, pos int) CompletionOutcome {
		return MenuOutcome([]Completion{{Text: "foo"}}, pos+3)
	}
	ed := newKeyEditor(t, Config{CompleteOutcomeFunc: complete}, "echo fo")
	press(t, ed, keyTab())
	if got := ed.state.Buffer.Content(); got != "echo foo" {
		t.Errorf("Tab on %q gives %q, want %q", "echo fo", got, "echo foo")
	}
}
