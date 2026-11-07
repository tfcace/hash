package editor

import "testing"

// A selection started in one buffer must not survive a history swap: the
// anchor would point at rows the new buffer does not have.
func TestSelection_HistorySwapResetsSelection(t *testing.T) {
	ed := newKeyEditor(t, Config{
		HistoryFunc: func(dir int, _ string) (string, bool) { return "single line", true },
	}, "one\ntwo\nthree")
	ed.mode = NewNormalMode()
	ed.state.Cursor.MoveTo(2, 0)

	press(t, ed, runes("vkkkd")...)

	if got := ed.state.Buffer.Content(); got != "single line" {
		t.Fatalf("buffer = %q, want the history entry untouched", got)
	}
	if ed.state.Cursor.HasSelection() {
		t.Error("selection survived the buffer swap")
	}
}

// Clamp keeps the anchor inside the buffer as well as the cursor.
func TestCursor_ClampClampsAnchor(t *testing.T) {
	cur := NewCursor()
	cur.MoveTo(2, 3)
	cur.StartSelection()
	buf := NewBufferFromString("x")
	cur.Clamp(buf)
	start, end := cur.SelectionRange(buf)
	if start.Row != 0 || end.Row != 0 || end.Col > 1 {
		t.Fatalf("selection range %v..%v escapes a one-line buffer", start, end)
	}
}

// Every motion extends an active selection, h/l included (Helix-like): v
// starts it and only Esc or ; ends it.
func TestSelection_MotionsExtend(t *testing.T) {
	for _, motion := range []string{"l", "h", "w", "b", "e", "0", "$"} {
		t.Run(motion, func(t *testing.T) {
			ed := newKeyEditor(t, Config{}, "alpha beta gamma")
			ed.mode = NewNormalMode()
			ed.state.Cursor.MoveTo(0, 6)
			press(t, ed, runes("v"+motion)...)
			if !ed.state.Cursor.HasSelection() {
				t.Fatalf("v then %s dropped the selection", motion)
			}
		})
	}
}

// d after v+l deletes the extended span, not nothing.
func TestSelection_VThenLDeletesSpan(t *testing.T) {
	ed := newKeyEditor(t, Config{}, "abcdef")
	ed.mode = NewNormalMode()
	ed.state.Cursor.MoveTo(0, 1)
	press(t, ed, runes("vlld")...)
	if got := ed.state.Buffer.Content(); got != "adef" {
		t.Fatalf("buffer = %q, want %q", got, "adef")
	}
}

// x turns a charwise selection into whole lines instead of treating it as a
// linewise one and jumping a row.
func TestSelection_XOnCharSelectionSelectsItsLines(t *testing.T) {
	ed := newKeyEditor(t, Config{}, "one\ntwo\nthree")
	ed.mode = NewNormalMode()
	ed.state.Cursor.MoveTo(0, 1)
	press(t, ed, runes("vxd")...)
	if got := ed.state.Buffer.Content(); got != "\ntwo\nthree" {
		t.Fatalf("buffer = %q, want the first line emptied", got)
	}
}

// x on a linewise selection extends it by a line, and the span stays whole
// lines even after a column motion.
func TestSelection_XExtendsLinewise(t *testing.T) {
	ed := newKeyEditor(t, Config{}, "one\ntwo\nthree")
	ed.mode = NewNormalMode()
	ed.state.Cursor.MoveTo(0, 1)
	press(t, ed, runes("xxhd")...)
	if got := ed.state.Buffer.Content(); got != "\nthree" {
		t.Fatalf("buffer = %q, want the first two lines removed", got)
	}
}
