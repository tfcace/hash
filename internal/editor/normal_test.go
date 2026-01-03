// internal/editor/normal_test.go
package editor

import "testing"

func TestNormalMode_Movement_h(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello")
	state.Cursor.MoveTo(0, 3)

	mode := NewNormalMode()
	mode.HandleKey(Key{Rune: 'h'}, state)

	if state.Cursor.Pos.Col != 2 {
		t.Errorf("Col = %d, want 2", state.Cursor.Pos.Col)
	}
}

func TestNormalMode_Movement_l(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello")
	state.Cursor.MoveTo(0, 2)

	mode := NewNormalMode()
	mode.HandleKey(Key{Rune: 'l'}, state)

	if state.Cursor.Pos.Col != 3 {
		t.Errorf("Col = %d, want 3", state.Cursor.Pos.Col)
	}
}

func TestNormalMode_Movement_w(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello world")
	state.Cursor.MoveTo(0, 0)

	mode := NewNormalMode()
	mode.HandleKey(Key{Rune: 'w'}, state)

	if state.Cursor.Pos.Col != 6 {
		t.Errorf("Col = %d, want 6 (start of 'world')", state.Cursor.Pos.Col)
	}
}

func TestNormalMode_Insert_i(t *testing.T) {
	state := NewEditorState()
	mode := NewNormalMode()

	result := mode.HandleKey(Key{Rune: 'i'}, state)

	if result.NewMode == nil {
		t.Error("Should switch to insert mode")
	}
	if _, ok := result.NewMode.(*InsertMode); !ok {
		t.Error("NewMode should be InsertMode")
	}
}

func TestNormalMode_Delete_d(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello")
	state.Cursor.MoveTo(0, 0)
	state.Cursor.StartSelection()
	state.Cursor.MoveTo(0, 5)

	mode := NewNormalMode()
	mode.HandleKey(Key{Rune: 'd'}, state)

	if state.Buffer.Content() != "" {
		t.Errorf("Content = %q, want empty", state.Buffer.Content())
	}
}

func TestNormalMode_SelectLine_x(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello\nworld")
	state.Cursor.MoveTo(0, 2)

	mode := NewNormalMode()
	mode.HandleKey(Key{Rune: 'x'}, state)

	if !state.Cursor.HasSelection() {
		t.Error("Should have selection after x")
	}
	start, end := state.Cursor.SelectionRange()
	if start.Col != 0 || end.Col != 5 {
		t.Errorf("Selection = %d-%d, want 0-5", start.Col, end.Col)
	}
}

func TestNormalMode_Enter_Submits(t *testing.T) {
	state := NewEditorState()
	mode := NewNormalMode()

	result := mode.HandleKey(Key{Special: KeyEnter}, state)

	if !result.Submit {
		t.Error("Enter in normal mode should submit")
	}
}

func TestNormalMode_Yank_y(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello")
	state.Cursor.MoveTo(0, 0)
	state.Cursor.StartSelection()
	state.Cursor.MoveTo(0, 5)

	mode := NewNormalMode()
	result := mode.HandleKey(Key{Rune: 'y'}, state)

	// Buffer should be unchanged
	if state.Buffer.Content() != "hello" {
		t.Errorf("Content = %q, want %q (unchanged)", state.Buffer.Content(), "hello")
	}
	_ = result // Yank to clipboard handled externally
}
