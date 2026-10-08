package editor

import (
	"testing"
)

func pasteInto(t *testing.T, mode Mode, state *EditorState, text string) {
	t.Helper()
	mode.HandleKey(Key{Special: KeyPaste, PasteText: text}, state)
}

func TestInsertMode_PasteMultilineLiterally(t *testing.T) {
	state := NewEditorState()
	state.LineContinuation = true // default config must not rewrite pasted text

	pasteInto(t, NewInsertMode(), state, "echo 'L1\nL2'")

	got := state.Buffer.Content()
	want := "echo 'L1\nL2'"
	if got != want {
		t.Errorf("pasted content = %q, want it inserted literally %q", got, want)
	}
	if state.Cursor.Pos.Row != 1 || state.Cursor.Pos.Col != len("L2'") {
		t.Errorf("cursor = (%d,%d), want end of pasted text (1,%d)",
			state.Cursor.Pos.Row, state.Cursor.Pos.Col, len("L2'"))
	}
}

func TestInsertMode_PasteTwoCommandsStayTwoCommands(t *testing.T) {
	state := NewEditorState()
	state.LineContinuation = true

	pasteInto(t, NewInsertMode(), state, "echo ONE\necho TWO")

	got := state.Buffer.Content()
	if got != "echo ONE\necho TWO" {
		t.Errorf("pasted script = %q; continuation injection would merge the commands", got)
	}
}

func TestInsertMode_PasteNormalizesLineEndings(t *testing.T) {
	state := NewEditorState()
	state.LineContinuation = true

	pasteInto(t, NewInsertMode(), state, "a\r\nb\rc")

	if got := state.Buffer.Content(); got != "a\nb\nc" {
		t.Errorf("content = %q, want CRLF and CR normalized to %q", got, "a\nb\nc")
	}
}

func TestInsertMode_PastePreservesTrailingBackslash(t *testing.T) {
	state := NewEditorState()
	state.LineContinuation = true

	pasteInto(t, NewInsertMode(), state, "cmd \\\narg")

	if got := state.Buffer.Content(); got != "cmd \\\narg" {
		t.Errorf("content = %q, want the user's own continuation untouched", got)
	}
}

func TestNormalMode_PasteInsertsLiterally(t *testing.T) {
	state := NewEditorState()
	state.LineContinuation = true

	pasteInto(t, NewNormalMode(), state, "one\ntwo")

	if got := state.Buffer.Content(); got != "one\ntwo" {
		t.Errorf("normal-mode paste content = %q, want %q (paste must not be ignored)", got, "one\ntwo")
	}
}

func TestInsertMode_PasteInvalidUTF8LeavesCursorAfterPaste(t *testing.T) {
	// A range loop yields U+FFFD (3 bytes wide as a string) for each invalid
	// byte, so counting len(string(r)) overshoots. The cursor must sit right
	// after the pasted bytes, before the existing text.
	state := NewEditorState()
	state.Buffer = NewBufferFromString("XYZ")
	state.Cursor.MoveTo(0, 0)

	pasteInto(t, NewInsertMode(), state, "ab\xffcd")

	if got := state.Buffer.Content(); got != "ab\xffcdXYZ" {
		t.Fatalf("content = %q, want %q", got, "ab\xffcdXYZ")
	}
	if state.Cursor.Pos.Row != 0 || state.Cursor.Pos.Col != 5 {
		t.Errorf("cursor = (%d,%d), want (0,5), just after the pasted bytes",
			state.Cursor.Pos.Row, state.Cursor.Pos.Col)
	}
}

func TestCursorAfterInsert(t *testing.T) {
	tests := []struct {
		name         string
		row, col     int
		text         string
		wantR, wantC int
	}{
		{"single line", 0, 3, "abc", 0, 6},
		{"invalid utf8 counts bytes", 0, 0, "ab\xffcd", 0, 5},
		{"multibyte rune counts bytes", 0, 1, "é", 0, 3},
		{"newline resets column", 2, 4, "ab\ncd", 3, 2},
		{"trailing newline", 0, 4, "ab\n", 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, c := cursorAfterInsert(tt.row, tt.col, tt.text)
			if r != tt.wantR || c != tt.wantC {
				t.Errorf("cursorAfterInsert(%d,%d,%q) = (%d,%d), want (%d,%d)",
					tt.row, tt.col, tt.text, r, c, tt.wantR, tt.wantC)
			}
		})
	}
}
