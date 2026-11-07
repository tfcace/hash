package editor

import (
	"strconv"
	"testing"
)

func typed(t *testing.T, ed *Editor, s string) {
	t.Helper()
	press(t, ed, runes(s)...)
}

// One burst of typing is one undo step, back to before its first character.
func TestUndo_TypingIsOneGroup(t *testing.T) {
	ed := newKeyEditor(t, Config{}, "")
	typed(t, ed, "abc")
	press(t, ed, keyEsc(), Key{Rune: 'u'})
	if got := ed.state.Buffer.Content(); got != "" {
		t.Fatalf("after u buffer = %q, want empty", got)
	}
}

// An edit made after an undo starts a new branch: redo has nothing left.
func TestUndo_EditAfterUndoTruncatesRedo(t *testing.T) {
	ed := newKeyEditor(t, Config{}, "abcdef")
	ed.setMode(NewNormalMode())
	ed.state.Cursor.MoveTo(0, 0)

	typed(t, ed, "vld")
	if got := ed.state.Buffer.Content(); got != "bcdef" {
		t.Fatalf("after first delete = %q", got)
	}
	typed(t, ed, "u$vhd")
	if got := ed.state.Buffer.Content(); got != "abcde" {
		t.Fatalf("after second delete = %q", got)
	}
	typed(t, ed, "U")
	if got := ed.state.Buffer.Content(); got != "abcde" {
		t.Fatalf("U resurrected an abandoned branch: %q", got)
	}
}

// Whole-word replacements are undoable steps like typing.
func TestUndo_BufferReplacementsAreUndoable(t *testing.T) {
	t.Run("ghost accept", func(t *testing.T) {
		ed := newKeyEditor(t, Config{}, "")
		typed(t, ed, "git ")
		setAgentGhost(ed.ghost, "status", false)
		press(t, ed, keyTab(), keyEsc(), Key{Rune: 'u'})
		if got := ed.state.Buffer.Content(); got != "git " {
			t.Fatalf("after u buffer = %q, want %q", got, "git ")
		}
	})
	t.Run("completion accept", func(t *testing.T) {
		ed := treeEditor(t, "")
		typed(t, ed, "cat l")
		press(t, ed, keyTab())
		if got := ed.state.Buffer.Content(); got != "cat lib/" {
			t.Fatalf("after Tab buffer = %q", got)
		}
		press(t, ed, keyEsc(), Key{Rune: 'u'})
		if got := ed.state.Buffer.Content(); got != "cat l" {
			t.Fatalf("after u buffer = %q, want %q", got, "cat l")
		}
	})
	t.Run("history", func(t *testing.T) {
		ed := newKeyEditor(t, Config{
			HistoryFunc: func(int, string) (string, bool) { return "older command", true },
		}, "")
		typed(t, ed, "draft")
		press(t, ed, Key{Special: KeyUp}, keyEsc(), Key{Rune: 'u'})
		if got := ed.state.Buffer.Content(); got != "draft" {
			t.Fatalf("after u buffer = %q, want %q", got, "draft")
		}
	})
}

// The undo history keeps the latest maxUndo steps: every step holds a copy
// of the whole buffer, so an unbounded history grew without limit in a long
// editing session.
func TestUndo_HistoryIsCapped(t *testing.T) {
	u := NewUndoStack()
	for i := range maxUndo + 500 {
		u.Push(snapshotOf(NewBufferFromString(strconv.Itoa(i)), NewCursor()))
	}
	if len(u.undo) != maxUndo {
		t.Fatalf("undo steps = %d, want %d", len(u.undo), maxUndo)
	}
	if got := u.undo[0].Buffer.Content(); got != "500" {
		t.Errorf("oldest kept step = %q, want %q (the latest steps stay)", got, "500")
	}
	last, _ := u.Undo(snapshotOf(NewBuffer(), NewCursor()))
	if got := last.Buffer.Content(); got != strconv.Itoa(maxUndo+499) {
		t.Errorf("undo returned %q, want the latest step", got)
	}
}
