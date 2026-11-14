package editor

import (
	"runtime"
	"strings"
	"testing"
)

// A key that does not edit costs no copy of the text. Each Left in a 1MB
// buffer used to join the buffer twice to see whether it changed (2MB and
// about 2.6ms per key).
func TestUndo_MotionDoesNotCopyTheText(t *testing.T) {
	ed := newKeyEditor(t, Config{}, strings.Repeat(strings.Repeat("x", 99)+"\n", 1<<20/100))
	press(t, ed, Key{Special: KeyLeft}) // warm up
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	const keys = 20
	for range keys {
		ed.handleKeyEvent(Key{Special: KeyLeft})
	}
	runtime.ReadMemStats(&after)
	if perKey := (after.TotalAlloc - before.TotalAlloc) / keys; perKey > 64<<10 {
		t.Errorf("a Left key allocated %d bytes in a 1MB buffer", perKey)
	}
}

// Undo history is bounded by the bytes it holds, not only by its step
// count: 1000 steps of a 10MB buffer held 10GB.
func TestUndo_HistoryIsBoundedByBytes(t *testing.T) {
	u := NewUndoStack()
	line := strings.Repeat("y", 1<<20)
	for i := range 100 {
		u.Push(Snapshot{Buffer: NewBufferFromString(line + string(rune('a'+i%26))), Cursor: NewCursor()})
	}
	if held := u.heldBytes(); held > maxUndoBytes {
		t.Errorf("undo holds %d bytes, cap %d", held, maxUndoBytes)
	}
	if !u.CanUndo() {
		t.Error("the newest step must stay undoable")
	}
	// The newest step survives even when it alone is over the cap.
	u.Push(Snapshot{Buffer: NewBufferFromString(strings.Repeat("z", maxUndoBytes+1)), Cursor: NewCursor()})
	if snap, ok := u.Undo(Snapshot{Buffer: NewBuffer(), Cursor: NewCursor()}); !ok || len(snap.Buffer.Content()) != maxUndoBytes+1 {
		t.Error("the step just pushed was dropped")
	}
}
