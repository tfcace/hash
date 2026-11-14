package editor

import (
	"io"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/shlex"
)

// newKeyEditor builds an editor with the given buffer content and the cursor
// at the end, ready to be driven by press.
func newKeyEditor(t *testing.T, cfg Config, text string) *Editor {
	t.Helper()
	ed := New(cfg, strings.NewReader(""), io.Discard)
	ed.SetInitialText(text)
	return ed
}

// press feeds keys to the editor as the event loop would (each key, then
// the redraw) and returns the result of the last key that ended the
// editor, if any.
func press(t *testing.T, ed *Editor, keys ...Key) (Result, bool) {
	t.Helper()
	for i, k := range keys {
		res, done := ed.handleKeyEvent(k)
		ed.redraw()
		if done {
			if i != len(keys)-1 {
				t.Fatalf("editor exited on key %d of %d (%s)", i+1, len(keys), keyString(k))
			}
			return res, true
		}
	}
	return Result{}, false
}

// runes turns typed text into rune keys.
func runes(s string) []Key {
	keys := make([]Key, 0, len(s))
	for _, r := range s {
		keys = append(keys, Key{Rune: r})
	}
	return keys
}
func keyBackspace() Key { return Key{Special: KeyBackspace} }
func keyTab() Key       { return Key{Special: KeyTab} }

func seq(groups ...[]Key) []Key {
	var out []Key
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func one(k Key) []Key { return []Key{k} }

func keyEsc() Key { return Key{Special: KeyEscape} }

// itemsOnly adapts a candidate list to CompleteOutcomeFunc: the items
// replace the word at the cursor, and an empty list reads as no matches.
func itemsOnly(fn func(line string, pos int) []Completion) func(string, int) CompletionOutcome {
	return func(line string, pos int) CompletionOutcome {
		return wordMenu(line, pos, fn(line, pos)...)
	}
}

// wordMenu is a menu of items that replace the word at pos in line.
func wordMenu(line string, pos int, items ...Completion) CompletionOutcome {
	return MenuOutcome(items, shlex.WordStart(line, pos))
}
