// internal/editor/undo.go
package editor

// Snapshot holds buffer and cursor state for undo.
type Snapshot struct {
	Buffer *Buffer
	Cursor *Cursor
}

// UndoStack manages undo/redo history.
type UndoStack struct {
	history []Snapshot
	index   int // Points to current state (next undo returns index-1)
}

// NewUndoStack creates an empty undo stack.
func NewUndoStack() *UndoStack {
	return &UndoStack{
		history: make([]Snapshot, 0, 100),
		index:   0,
	}
}

// Push adds a new state to the history, clearing any redo states.
func (u *UndoStack) Push(buf *Buffer, cur *Cursor) {
	// Truncate any redo history
	u.history = u.history[:u.index]

	// Clone to avoid mutation
	u.history = append(u.history, Snapshot{
		Buffer: buf.Clone(),
		Cursor: cur.Clone(),
	})
	u.index = len(u.history)
}

// CanUndo returns true if there's state to undo to.
func (u *UndoStack) CanUndo() bool {
	return u.index > 1 // Need at least 2 states to undo
}

// CanRedo returns true if there's state to redo to.
func (u *UndoStack) CanRedo() bool {
	return u.index < len(u.history)
}

// Undo returns the previous state.
func (u *UndoStack) Undo() (*Buffer, *Cursor) {
	if !u.CanUndo() {
		return nil, nil
	}
	u.index--
	snap := u.history[u.index-1]
	return snap.Buffer.Clone(), snap.Cursor.Clone()
}

// Redo returns the next state.
func (u *UndoStack) Redo() (*Buffer, *Cursor) {
	if !u.CanRedo() {
		return nil, nil
	}
	snap := u.history[u.index]
	u.index++
	return snap.Buffer.Clone(), snap.Cursor.Clone()
}
