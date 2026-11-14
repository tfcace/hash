// internal/editor/undo.go
package editor

// Snapshot holds buffer and cursor state for undo.
type Snapshot struct {
	Buffer *Buffer
	Cursor *Cursor
}

// snapshotOf copies the buffer and cursor so later edits cannot reach it.
func snapshotOf(buf *Buffer, cur *Cursor) Snapshot {
	return Snapshot{Buffer: buf.Clone(), Cursor: cur.Clone()}
}

// UndoStack manages undo/redo history. undo holds the states before each
// edit group, most recent last; redo holds the states undone since the last
// edit.
type UndoStack struct {
	undo      []Snapshot
	redo      []Snapshot
	undoBytes int // text held by undo, counted as if no two steps shared a line
}

// NewUndoStack creates an empty undo stack.
func NewUndoStack() *UndoStack {
	return &UndoStack{}
}

// maxUndo is how many edit groups can be undone, and maxUndoBytes how much
// text their snapshots may hold; the oldest go first. Each step holds the
// whole buffer (sharing unchanged lines), so a big buffer edited often would
// otherwise hold gigabytes.
const (
	maxUndo      = 1000
	maxUndoBytes = 32 << 20
)

// Push records the state from before an edit group. A new edit abandons
// whatever was undone, so redo history is cleared. The step just pushed is
// always kept, even alone over the byte cap.
func (u *UndoStack) Push(s Snapshot) {
	u.undo = append(u.undo, s)
	u.undoBytes += s.Buffer.size()
	drop := 0
	for len(u.undo)-drop > 1 && (len(u.undo)-drop > maxUndo || u.undoBytes > maxUndoBytes) {
		u.undoBytes -= u.undo[drop].Buffer.size()
		drop++
	}
	if drop > 0 {
		u.undo = append(u.undo[:0], u.undo[drop:]...)
		clear(u.undo[len(u.undo) : len(u.undo)+drop])
	}
	u.redo = nil
}

// heldBytes is the text the undo steps hold.
func (u *UndoStack) heldBytes() int {
	return u.undoBytes
}

// CanUndo returns true if there's state to undo to.
func (u *UndoStack) CanUndo() bool {
	return len(u.undo) > 0
}

// CanRedo returns true if there's state to redo to.
func (u *UndoStack) CanRedo() bool {
	return len(u.redo) > 0
}

// Undo returns the state before the latest edit group and saves current for
// Redo. ok is false when there is nothing to undo.
func (u *UndoStack) Undo(current Snapshot) (Snapshot, bool) {
	if !u.CanUndo() {
		return Snapshot{}, false
	}
	prev := u.undo[len(u.undo)-1]
	u.undo = u.undo[:len(u.undo)-1]
	u.undoBytes -= prev.Buffer.size()
	u.redo = append(u.redo, current)
	return prev, true
}

// Redo returns the most recently undone state and saves current for Undo.
// ok is false when there is nothing to redo.
func (u *UndoStack) Redo(current Snapshot) (Snapshot, bool) {
	if !u.CanRedo() {
		return Snapshot{}, false
	}
	next := u.redo[len(u.redo)-1]
	u.redo = u.redo[:len(u.redo)-1]
	u.undo = append(u.undo, current)
	u.undoBytes += current.Buffer.size()
	return next, true
}
