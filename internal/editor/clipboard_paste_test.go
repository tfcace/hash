package editor

import "testing"

// p/P put the cursor right after the pasted text, multi-line pastes too.
func TestPasteText_CursorAfterMultilinePaste(t *testing.T) {
	ed := newKeyEditor(t, Config{}, "xy")
	ed.state.Cursor.MoveTo(0, 0)

	ed.pasteText("ab\ncd", true)

	if got := ed.state.Buffer.Content(); got != "ab\ncdxy" {
		t.Fatalf("buffer = %q", got)
	}
	if got := ed.state.Cursor.Pos; got != (Position{Row: 1, Col: 2}) {
		t.Fatalf("cursor = %+v, want {1 2} (after \"cd\")", got)
	}
}

// p pastes after the character under the cursor.
func TestPasteText_AfterCursor(t *testing.T) {
	ed := newKeyEditor(t, Config{}, "xy")
	ed.state.Cursor.MoveTo(0, 0)

	ed.pasteText("é", false)

	if got := ed.state.Buffer.Content(); got != "xéy" {
		t.Fatalf("buffer = %q", got)
	}
	if got := ed.state.Cursor.Pos; got != (Position{Row: 0, Col: len("xé")}) {
		t.Fatalf("cursor = %+v", got)
	}
}
