package editor

import (
	"io"
	"strings"
	"testing"
)

func newTestEditorWithText(t *testing.T, text string) *Editor {
	t.Helper()
	ed := New(Config{}, strings.NewReader(""), io.Discard)
	ed.state.Buffer = NewBufferFromString(text)
	return ed
}

func TestParseKey_Vt220TildeSequences(t *testing.T) {
	tests := []struct {
		seq  string
		want Key
	}{
		{"\x1b[1~", Key{Special: KeyHome}},
		{"\x1b[7~", Key{Special: KeyHome}},
		{"\x1b[4~", Key{Special: KeyEnd}},
		{"\x1b[8~", Key{Special: KeyEnd}},
		{"\x1b[3~", Key{Special: KeyDelete}}, // regression: already supported
		{"\x1b[5~", Key{Special: KeyPageUp}},
		{"\x1b[6~", Key{Special: KeyPageDown}},
		// Insert and function keys are discarded, never treated as Escape
		{"\x1b[2~", Key{}},
		{"\x1b[15~", Key{}},
		{"\x1b[24~", Key{}},
	}
	for _, tt := range tests {
		got := ParseKey([]byte(tt.seq))
		if got != tt.want {
			t.Errorf("ParseKey(%q) = %+v, want %+v", tt.seq, got, tt.want)
		}
	}
}

func TestParseKey_Vt220TildeWithModifier(t *testing.T) {
	got := ParseKey([]byte("\x1b[1;2~")) // Shift+Home
	if got.Special != KeyHome || !got.Shift {
		t.Errorf("ParseKey(ESC[1;2~) = %+v, want Shift+Home", got)
	}
	got = ParseKey([]byte("\x1b[3;3~")) // Alt+Delete
	if got.Special != KeyDelete || !got.Alt {
		t.Errorf("ParseKey(ESC[3;3~) = %+v, want Alt+Delete", got)
	}
}

func TestEditor_Vt220HomeActsWithoutModeSwitch(t *testing.T) {
	ed := newTestEditorWithText(t, "hello")
	ed.state.Cursor.MoveTo(0, 5)

	ed.handleKeyEvent(ParseKey([]byte("\x1b[1~")))

	if ed.mode.Name() != "insert" {
		t.Fatalf("mode = %q, want insert (vt220 Home must not act as Escape)", ed.mode.Name())
	}
	if ed.state.Cursor.Pos.Col != 0 {
		t.Errorf("cursor col = %d, want 0 (Home)", ed.state.Cursor.Pos.Col)
	}
}

func TestEditor_FunctionKeyIsIgnoredWithoutModeSwitch(t *testing.T) {
	ed := newTestEditorWithText(t, "hello")
	ed.state.Cursor.MoveTo(0, 3)

	ed.handleKeyEvent(ParseKey([]byte("\x1b[15~"))) // F5 on many terminals

	if ed.mode.Name() != "insert" {
		t.Fatalf("mode = %q, want insert (F-keys must not act as Escape)", ed.mode.Name())
	}
	if ed.state.Cursor.Pos.Col != 3 {
		t.Errorf("cursor col = %d, want unchanged 3", ed.state.Cursor.Pos.Col)
	}
}
