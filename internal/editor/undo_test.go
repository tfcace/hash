// internal/editor/undo_test.go
package editor

import "testing"

func snap(s string) Snapshot { return snapshotOf(NewBufferFromString(s), NewCursor()) }

func TestUndoStack_Empty(t *testing.T) {
	u := NewUndoStack()
	if u.CanUndo() {
		t.Error("CanUndo() = true on empty stack")
	}
	if u.CanRedo() {
		t.Error("CanRedo() = true on empty stack")
	}
}

func TestUndoStack_UndoRedoRoundTrip(t *testing.T) {
	u := NewUndoStack()
	u.Push(snap(""))      // before typing "hello"
	u.Push(snap("hello")) // before typing " world"

	got, ok := u.Undo(snap("hello world"))
	if !ok || got.Buffer.Content() != "hello" {
		t.Fatalf("Undo = %q, %v; want hello", got.Buffer.Content(), ok)
	}
	got, ok = u.Undo(got)
	if !ok || got.Buffer.Content() != "" {
		t.Fatalf("second Undo = %q, %v; want empty", got.Buffer.Content(), ok)
	}
	if u.CanUndo() {
		t.Error("CanUndo() after undoing everything")
	}

	got, ok = u.Redo(got)
	if !ok || got.Buffer.Content() != "hello" {
		t.Fatalf("Redo = %q, %v; want hello", got.Buffer.Content(), ok)
	}
	got, ok = u.Redo(got)
	if !ok || got.Buffer.Content() != "hello world" {
		t.Fatalf("second Redo = %q, %v; want hello world", got.Buffer.Content(), ok)
	}
	if u.CanRedo() {
		t.Error("CanRedo() after redoing everything")
	}
}

func TestUndoStack_PushClearsRedo(t *testing.T) {
	u := NewUndoStack()
	u.Push(snap("a"))
	u.Undo(snap("b"))

	// New push should clear redo history
	u.Push(snap("a"))

	if u.CanRedo() {
		t.Error("CanRedo() = true after push (should be cleared)")
	}
}
