package editor

import (
	"testing"
)

func TestNextWordStart_PunctuationBoundaries(t *testing.T) {
	line := "--flag=path/to/file.txt"
	stops := []int{}
	col := 0
	for col < len(line) {
		col = nextWordStart(line, col)
		stops = append(stops, col)
	}
	want := []int{2, 7, 12, 15, 20, 23}
	if len(stops) != len(want) {
		t.Fatalf("stops = %v, want %v", stops, want)
	}
	for i := range want {
		if stops[i] != want[i] {
			t.Fatalf("stops = %v, want %v", stops, want)
		}
	}
}

func TestPrevWordStart_PunctuationBoundaries(t *testing.T) {
	line := "--flag=path/to/file.txt"
	stops := []int{}
	col := len(line)
	for col > 0 {
		col = prevWordStart(line, col)
		stops = append(stops, col)
	}
	want := []int{20, 15, 12, 7, 2, 0}
	if len(stops) != len(want) {
		t.Fatalf("stops = %v, want %v", stops, want)
	}
	for i := range want {
		if stops[i] != want[i] {
			t.Fatalf("stops = %v, want %v", stops, want)
		}
	}
}

func TestWordMotion_UnderscoreIsNotABoundary(t *testing.T) {
	line := "foo_bar baz"
	if got := nextWordStart(line, 0); got != 8 {
		t.Errorf("nextWordStart(%q, 0) = %d, want 8 (snake_case is one word)", line, got)
	}
	if got := prevWordStart(line, len(line)); got != 8 {
		t.Errorf("prevWordStart at end = %d, want 8", got)
	}
	if got := prevWordStart(line, 8); got != 0 {
		t.Errorf("prevWordStart(8) = %d, want 0 (whole foo_bar)", got)
	}
}

func TestWordMotion_MultibyteSeparators(t *testing.T) {
	line := "a«b»c" // « and » are separators and multibyte
	if got := nextWordStart(line, 0); got != 3 {
		t.Errorf("nextWordStart = %d, want 3 (byte offset of b)", got)
	}
	if got := nextWordStart(line, 3); got != 6 {
		t.Errorf("nextWordStart from b = %d, want 6 (byte offset of c)", got)
	}
}

func TestInsertMode_AltRightStopsAtPunctuation(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("git log --since=yesterday")
	state.Cursor.MoveTo(0, 0)
	mode := NewInsertMode()

	mode.HandleKey(Key{Special: KeyRight, Alt: true}, state) // to "log"
	if state.Cursor.Pos.Col != 4 {
		t.Fatalf("first stop = %d, want 4", state.Cursor.Pos.Col)
	}
	mode.HandleKey(Key{Special: KeyRight, Alt: true}, state) // to "--since..." word start "since"
	if state.Cursor.Pos.Col != 10 {
		t.Fatalf("second stop = %d, want 10 (start of 'since', past '--')", state.Cursor.Pos.Col)
	}
	mode.HandleKey(Key{Special: KeyRight, Alt: true}, state) // to "yesterday"
	if state.Cursor.Pos.Col != 16 {
		t.Fatalf("third stop = %d, want 16 (start of 'yesterday', past '=')", state.Cursor.Pos.Col)
	}
}

func TestInsertMode_CtrlWStaysWhitespaceDelimited(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("cat path/to/file")
	state.Cursor.MoveTo(0, len("cat path/to/file"))
	mode := NewInsertMode()

	mode.HandleKey(Key{Rune: 'w', Ctrl: true}, state)

	if got := state.Buffer.Content(); got != "cat " {
		t.Errorf("after Ctrl+W content = %q, want %q (whole whitespace-delimited word deleted)", got, "cat ")
	}
}

func TestNormalMode_WordMotionsPunctuationAware(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("a/b.c dd")
	state.Cursor.MoveTo(0, 0)
	mode := NewNormalMode()

	mode.HandleKey(Key{Rune: 'w'}, state) // past 'a', past '/', at 'b'
	if state.Cursor.Pos.Col != 2 {
		t.Fatalf("w = %d, want 2", state.Cursor.Pos.Col)
	}
	mode.HandleKey(Key{Rune: 'e'}, state) // end of 'b' is itself; e advances to end of next word 'c'
	if state.Cursor.Pos.Col != 4 {
		t.Fatalf("e = %d, want 4 (on 'c')", state.Cursor.Pos.Col)
	}
	state.Cursor.MoveTo(0, 8)
	mode.HandleKey(Key{Rune: 'b'}, state) // back to start of "dd"
	if state.Cursor.Pos.Col != 6 {
		t.Fatalf("b = %d, want 6", state.Cursor.Pos.Col)
	}
	mode.HandleKey(Key{Rune: 'b'}, state) // back to start of "c"
	if state.Cursor.Pos.Col != 4 {
		t.Fatalf("second b = %d, want 4", state.Cursor.Pos.Col)
	}
}
