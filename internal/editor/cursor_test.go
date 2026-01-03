// internal/editor/cursor_test.go
package editor

import "testing"

func TestCursor_New(t *testing.T) {
	c := NewCursor()
	if c.Pos.Row != 0 || c.Pos.Col != 0 {
		t.Errorf("Pos = %+v, want {0, 0}", c.Pos)
	}
	if c.HasSelection() {
		t.Error("HasSelection() = true, want false")
	}
}

func TestCursor_MoveTo(t *testing.T) {
	c := NewCursor()
	c.MoveTo(2, 5)
	if c.Pos.Row != 2 || c.Pos.Col != 5 {
		t.Errorf("Pos = %+v, want {2, 5}", c.Pos)
	}
}

func TestCursor_Selection(t *testing.T) {
	c := NewCursor()
	c.MoveTo(0, 5)
	c.StartSelection()
	c.MoveTo(0, 10)

	if !c.HasSelection() {
		t.Error("HasSelection() = false, want true")
	}

	start, end := c.SelectionRange()
	if start.Col != 5 || end.Col != 10 {
		t.Errorf("SelectionRange() = %+v, %+v, want {0,5}, {0,10}", start, end)
	}
}

func TestCursor_SelectionRange_Backwards(t *testing.T) {
	c := NewCursor()
	c.MoveTo(0, 10)
	c.StartSelection()
	c.MoveTo(0, 5)

	start, end := c.SelectionRange()
	if start.Col != 5 || end.Col != 10 {
		t.Errorf("SelectionRange() should normalize: got %+v, %+v", start, end)
	}
}

func TestCursor_ClearSelection(t *testing.T) {
	c := NewCursor()
	c.StartSelection()
	c.MoveTo(0, 5)
	c.ClearSelection()

	if c.HasSelection() {
		t.Error("HasSelection() = true after clear")
	}
}

func TestCursor_Clamp(t *testing.T) {
	buf := NewBufferFromString("hello\nworld")
	c := NewCursor()
	c.MoveTo(5, 100) // Way out of bounds
	c.Clamp(buf)

	if c.Pos.Row != 1 {
		t.Errorf("Row = %d, want 1 (clamped)", c.Pos.Row)
	}
	if c.Pos.Col != 5 {
		t.Errorf("Col = %d, want 5 (clamped to line length)", c.Pos.Col)
	}
}
