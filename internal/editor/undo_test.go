// internal/editor/undo_test.go
package editor

import "testing"

func TestUndoStack_Empty(t *testing.T) {
	u := NewUndoStack()
	if u.CanUndo() {
		t.Error("CanUndo() = true on empty stack")
	}
	if u.CanRedo() {
		t.Error("CanRedo() = true on empty stack")
	}
}

func TestUndoStack_PushAndUndo(t *testing.T) {
	u := NewUndoStack()

	buf1 := NewBufferFromString("hello")
	cur1 := NewCursor()
	u.Push(buf1, cur1)

	buf2 := NewBufferFromString("hello world")
	cur2 := NewCursor()
	cur2.MoveTo(0, 11)
	u.Push(buf2, cur2)

	if !u.CanUndo() {
		t.Error("CanUndo() = false after push")
	}

	buf, cur := u.Undo()
	if buf.Content() != "hello" {
		t.Errorf("Undo content = %q, want %q", buf.Content(), "hello")
	}
	if cur.Pos.Col != 0 {
		t.Errorf("Undo cursor col = %d, want 0", cur.Pos.Col)
	}
}

func TestUndoStack_Redo(t *testing.T) {
	u := NewUndoStack()

	buf1 := NewBufferFromString("hello")
	u.Push(buf1, NewCursor())

	buf2 := NewBufferFromString("hello world")
	u.Push(buf2, NewCursor())

	u.Undo()

	if !u.CanRedo() {
		t.Error("CanRedo() = false after undo")
	}

	buf, _ := u.Redo()
	if buf.Content() != "hello world" {
		t.Errorf("Redo content = %q, want %q", buf.Content(), "hello world")
	}
}

func TestUndoStack_PushClearsRedo(t *testing.T) {
	u := NewUndoStack()

	u.Push(NewBufferFromString("a"), NewCursor())
	u.Push(NewBufferFromString("b"), NewCursor())
	u.Undo()

	// New push should clear redo history
	u.Push(NewBufferFromString("c"), NewCursor())

	if u.CanRedo() {
		t.Error("CanRedo() = true after push (should be cleared)")
	}
}
