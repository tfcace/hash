package editor

import "testing"

// Ctrl+C on a typed line in normal mode clears it and returns to insert, so
// the next word is typed rather than run as motions.
func TestMode_CtrlCInNormalModeReturnsToInsert(t *testing.T) {
	ed := newKeyEditor(t, Config{}, "git status")
	press(t, ed, keyEsc(), Key{Rune: 'c', Ctrl: true})
	press(t, ed, runes("ls")...)
	if got := ed.state.Buffer.Content(); got != "ls" {
		t.Fatalf("buffer = %q, want %q typed after Ctrl+C", got, "ls")
	}
}

// Normal-mode Ctrl+R / Ctrl+P honor the same switches as insert mode.
func TestMode_NormalModeHonorsDisabledPickers(t *testing.T) {
	ed := newKeyEditor(t, Config{DisableHistorySearch: true, DisableContextPicker: true}, "ls")
	press(t, ed, keyEsc())
	for _, r := range []rune{'r', 'p'} {
		if _, done := press(t, ed, Key{Rune: r, Ctrl: true}); done {
			t.Errorf("Ctrl+%c left the editor although it is disabled", r)
		}
	}
}

// Changing mode drops the selection: it belonged to the old mode's motions.
func TestMode_ModeChangeClearsSelection(t *testing.T) {
	ed := newKeyEditor(t, Config{}, "abcdef")
	press(t, ed, keyEsc())
	ed.state.Cursor.MoveTo(0, 1)
	press(t, ed, runes("vlli")...)
	if ed.state.Cursor.HasSelection() {
		t.Fatal("selection survived the switch to insert mode")
	}
	press(t, ed, runes("X")...)
	if got := ed.state.Buffer.Content(); got != "abcXdef" {
		t.Fatalf("buffer = %q, want X inserted, not replacing the old selection", got)
	}
}
