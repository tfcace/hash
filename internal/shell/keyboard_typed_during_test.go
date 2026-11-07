package shell

import (
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/editor"
)

// The keys a command left unread come back for the next prompt, all of
// them, as the terminal queued them (an Enter as the newline the terminal
// made of it). A line without its Enter stays to be edited.
func TestKeyboard_TypedDuringHoldsEveryKey(t *testing.T) {
	for _, tc := range []struct{ typed, want string }{
		{"echo TA2\n", "echo TA2⏎"},
		{"echo TA2\npartial", "echo TA2⏎partial"},
		{"a\nb\n", "a⏎b⏎"},
		{"partial", "partial"},
		{"", ""},
	} {
		kb := keyboard{keys: editor.NewInputReader(strings.NewReader(""))}
		kb.typedDuring([]byte(tc.typed))
		var got strings.Builder
		for _, k := range kb.keys.Typeahead() {
			switch k.Special {
			case editor.KeyEnter:
				got.WriteString("⏎")
			case editor.KeyNone:
				got.WriteRune(k.Rune)
			default:
				got.WriteString("?")
			}
		}
		if got.String() != tc.want {
			t.Errorf("typed %q: next prompt gets %q, want %q", tc.typed, got.String(), tc.want)
		}
	}
}

// Keys for the next prompt are held in two places: the executor keeps what
// a line's PTY commands left unread (all of it), and the key reader holds
// what a turn's watcher or a question read and did not use (up to the first
// Enter). The prompt reads the executor's first (typedDuring puts them in
// front), which is the order they were typed in only because no line runs
// on a PTY once a reader holds keys: a turn the user typed past runs no
// command (its hint is declined at once), and a line's question is its last
// step. (The one exception is the first-run migration question, whose import
// sources the rc files after it.) So the executor's keys, typed while
// `cmd | ??` ran its command, come before the keys held from the reply that
// followed.
func TestKeyboard_TypedDuringComesBeforeTheKeysAReaderHeld(t *testing.T) {
	kb := keyboard{keys: editor.NewInputReader(strings.NewReader(""))}
	holdForNextPrompt(kb.keys, []editor.Key{{Rune: 'a'}, {Rune: 'b'}}) // typed during the reply
	kb.typedDuring([]byte("xy"))                                       // typed during the pipe's command, before it
	var got strings.Builder
	for _, k := range kb.keys.Typeahead() {
		got.WriteRune(k.Rune)
	}
	if got.String() != "xyab" {
		t.Errorf("next prompt gets %q, want %q", got.String(), "xyab")
	}
}
