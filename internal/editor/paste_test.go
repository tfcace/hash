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
