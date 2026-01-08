// internal/editor/insert_test.go
package editor

import "testing"

func TestInsertMode_Printable(t *testing.T) {
	state := NewEditorState()
	mode := NewInsertMode()

	result := mode.HandleKey(Key{Rune: 'h'}, state)

	if state.Buffer.Content() != "h" {
		t.Errorf("Content = %q, want %q", state.Buffer.Content(), "h")
	}
	if result.NewMode != nil {
		t.Error("Should stay in insert mode")
	}
}

func TestInsertMode_Backspace(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello")
	state.Cursor.MoveTo(0, 5)

	mode := NewInsertMode()
	mode.HandleKey(Key{Special: KeyBackspace}, state)

	if state.Buffer.Content() != "hell" {
		t.Errorf("Content = %q, want %q", state.Buffer.Content(), "hell")
	}
}

func TestInsertMode_Enter_Submits(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello")
	state.Cursor.MoveTo(0, 5)

	mode := NewInsertMode()
	result := mode.HandleKey(Key{Special: KeyEnter}, state)

	if !result.Submit {
		t.Error("Enter should submit")
	}
}

func TestInsertMode_ShiftEnter_InsertsNewline(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello")
	state.Cursor.MoveTo(0, 5)

	mode := NewInsertMode()
	result := mode.HandleKey(Key{Special: KeyEnter, Shift: true}, state)

	if result.Submit {
		t.Error("Shift+Enter should not submit")
	}
	if state.Buffer.LineCount() != 2 {
		t.Errorf("LineCount = %d, want 2", state.Buffer.LineCount())
	}
}

func TestInsertMode_Escape_ToNormalMode(t *testing.T) {
	state := NewEditorState()
	mode := NewInsertMode()

	result := mode.HandleKey(Key{Special: KeyEscape}, state)

	if result.NewMode == nil {
		t.Error("Should switch to normal mode")
	}
	if _, ok := result.NewMode.(*NormalMode); !ok {
		t.Error("NewMode should be NormalMode")
	}
}

func TestInsertMode_CtrlA_LineStart(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello")
	state.Cursor.MoveTo(0, 5)

	mode := NewInsertMode()
	mode.HandleKey(Key{Rune: 'a', Ctrl: true}, state)

	if state.Cursor.Pos.Col != 0 {
		t.Errorf("Cursor col = %d, want 0", state.Cursor.Pos.Col)
	}
}

func TestInsertMode_CtrlE_LineEnd(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello")
	state.Cursor.MoveTo(0, 0)

	mode := NewInsertMode()
	mode.HandleKey(Key{Rune: 'e', Ctrl: true}, state)

	if state.Cursor.Pos.Col != 5 {
		t.Errorf("Cursor col = %d, want 5", state.Cursor.Pos.Col)
	}
}

func TestInsertMode_CtrlP_ContextPicker(t *testing.T) {
	mode := NewInsertMode()
	state := NewEditorState()
	state.Buffer.Insert(0, 0, "hello")
	state.Cursor.MoveTo(0, 5)

	// Ctrl+P should trigger context picker
	key := Key{Ctrl: true, Rune: 'p'}
	result := mode.HandleKey(key, state)

	if !result.ContextPicker {
		t.Error("Ctrl+P should set ContextPicker=true")
	}
	// Cursor should not move
	if state.Cursor.Pos.Col != 5 {
		t.Errorf("cursor should stay at col 5, got %d", state.Cursor.Pos.Col)
	}
}
