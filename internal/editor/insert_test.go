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

func TestEndsWithBackslash(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"hello", false},
		{"hello\\", true},
		{"hello \\", true},
		{"hello \\ ", true},  // Trailing spaces don't matter
		{"hello \\  ", true}, // Multiple trailing spaces
		{"", false},
		{"\\", true},
		{" \\ ", true},
	}
	for _, tt := range tests {
		got := endsWithBackslash(tt.input)
		if got != tt.want {
			t.Errorf("endsWithBackslash(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"hello", []string{"hello"}},
		{"hello\nworld", []string{"hello", "world"}},
		{"a\nb\nc", []string{"a", "b", "c"}},
		{"", []string{""}},
		{"\n", []string{"", ""}},
		{"hello\r\nworld", []string{"hello", "world"}}, // Windows line endings
		{"hello\rworld", []string{"hello", "world"}},   // Old Mac line endings
	}
	for _, tt := range tests {
		got := splitLines(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("splitLines(%q) returned %d lines, want %d", tt.input, len(got), len(tt.want))
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitLines(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestAddLineContinuations(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},                     // Single line unchanged
		{"hello\nworld", "hello \\\nworld"},    // Two lines
		{"a\nb\nc", "a \\\nb \\\nc"},           // Three lines
		{"hello \\\nworld", "hello \\\nworld"}, // Already has continuation
		{"hello\\\nworld", "hello\\\nworld"},   // Continuation without space
		{"", ""},                               // Empty string
		{"hello\n", "hello \\\n"},              // Trailing newline
	}
	for _, tt := range tests {
		got := addLineContinuations(tt.input)
		if got != tt.want {
			t.Errorf("addLineContinuations(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestInsertMode_Paste(t *testing.T) {
	state := NewEditorState()
	mode := NewInsertMode()

	// Paste multiline content
	result := mode.HandleKey(Key{Special: KeyPaste, PasteText: "echo hello\necho world"}, state)

	if result.Action != ActionPaste {
		t.Errorf("Action = %v, want ActionPaste", result.Action)
	}

	expected := "echo hello \\\necho world"
	if state.Buffer.Content() != expected {
		t.Errorf("Content = %q, want %q", state.Buffer.Content(), expected)
	}

	// Cursor should be at end
	if state.Cursor.Pos.Row != 1 || state.Cursor.Pos.Col != 10 {
		t.Errorf("Cursor = (%d, %d), want (1, 10)", state.Cursor.Pos.Row, state.Cursor.Pos.Col)
	}
}

func TestInsertMode_ShiftRight_StartsSelection(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello")
	state.Cursor.MoveTo(0, 0)

	mode := NewInsertMode()
	mode.HandleKey(Key{Special: KeyRight, Shift: true}, state)

	if !state.Cursor.HasSelection() {
		t.Fatal("Shift+Right should start selection")
	}
	if state.Cursor.Pos.Col != 1 {
		t.Errorf("Cursor col = %d, want 1", state.Cursor.Pos.Col)
	}
	start, end := state.Cursor.SelectionRange()
	if start.Col != 0 || end.Col != 1 {
		t.Errorf("SelectionRange = (%d,%d), want (0,1)", start.Col, end.Col)
	}
}

func TestInsertMode_ShiftRight_ExtendsSelection(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello")
	state.Cursor.MoveTo(0, 0)

	mode := NewInsertMode()
	mode.HandleKey(Key{Special: KeyRight, Shift: true}, state)
	mode.HandleKey(Key{Special: KeyRight, Shift: true}, state)
	mode.HandleKey(Key{Special: KeyRight, Shift: true}, state)

	start, end := state.Cursor.SelectionRange()
	if start.Col != 0 || end.Col != 3 {
		t.Errorf("SelectionRange = (%d,%d), want (0,3)", start.Col, end.Col)
	}
}

func TestInsertMode_ShiftLeft_StartsSelection(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello")
	state.Cursor.MoveTo(0, 3)

	mode := NewInsertMode()
	mode.HandleKey(Key{Special: KeyLeft, Shift: true}, state)

	if !state.Cursor.HasSelection() {
		t.Fatal("Shift+Left should start selection")
	}
	start, end := state.Cursor.SelectionRange()
	if start.Col != 2 || end.Col != 3 {
		t.Errorf("SelectionRange = (%d,%d), want (2,3)", start.Col, end.Col)
	}
}

func TestInsertMode_ShiftUp_MultiLineSelection(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello\nworld")
	state.Cursor.MoveTo(1, 3)

	mode := NewInsertMode()
	mode.HandleKey(Key{Special: KeyUp, Shift: true}, state)

	if !state.Cursor.HasSelection() {
		t.Fatal("Shift+Up should start selection")
	}
	if state.Cursor.Pos.Row != 0 {
		t.Errorf("Cursor row = %d, want 0", state.Cursor.Pos.Row)
	}
}

func TestInsertMode_ShiftDown_MultiLineSelection(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello\nworld")
	state.Cursor.MoveTo(0, 2)

	mode := NewInsertMode()
	mode.HandleKey(Key{Special: KeyDown, Shift: true}, state)

	if !state.Cursor.HasSelection() {
		t.Fatal("Shift+Down should start selection")
	}
	if state.Cursor.Pos.Row != 1 {
		t.Errorf("Cursor row = %d, want 1", state.Cursor.Pos.Row)
	}
}

func TestInsertMode_ShiftHome_SelectsToLineStart(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello")
	state.Cursor.MoveTo(0, 3)

	mode := NewInsertMode()
	mode.HandleKey(Key{Special: KeyHome, Shift: true}, state)

	if !state.Cursor.HasSelection() {
		t.Fatal("Shift+Home should start selection")
	}
	start, end := state.Cursor.SelectionRange()
	if start.Col != 0 || end.Col != 3 {
		t.Errorf("SelectionRange = (%d,%d), want (0,3)", start.Col, end.Col)
	}
}

func TestInsertMode_ShiftEnd_SelectsToLineEnd(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello")
	state.Cursor.MoveTo(0, 1)

	mode := NewInsertMode()
	mode.HandleKey(Key{Special: KeyEnd, Shift: true}, state)

	if !state.Cursor.HasSelection() {
		t.Fatal("Shift+End should start selection")
	}
	start, end := state.Cursor.SelectionRange()
	if start.Col != 1 || end.Col != 5 {
		t.Errorf("SelectionRange = (%d,%d), want (1,5)", start.Col, end.Col)
	}
}

func TestInsertMode_ShiftAltRight_SelectsWord(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello world")
	state.Cursor.MoveTo(0, 0)

	mode := NewInsertMode()
	mode.HandleKey(Key{Special: KeyRight, Shift: true, Alt: true}, state)

	if !state.Cursor.HasSelection() {
		t.Fatal("Shift+Alt+Right should start selection")
	}
	start, end := state.Cursor.SelectionRange()
	if start.Col != 0 || end.Col != 6 {
		t.Errorf("SelectionRange = (%d,%d), want (0,6)", start.Col, end.Col)
	}
}

func TestInsertMode_ShiftAltLeft_SelectsWordBack(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello world")
	state.Cursor.MoveTo(0, 11)

	mode := NewInsertMode()
	mode.HandleKey(Key{Special: KeyLeft, Shift: true, Alt: true}, state)

	if !state.Cursor.HasSelection() {
		t.Fatal("Shift+Alt+Left should start selection")
	}
	start, end := state.Cursor.SelectionRange()
	if start.Col != 6 || end.Col != 11 {
		t.Errorf("SelectionRange = (%d,%d), want (6,11)", start.Col, end.Col)
	}
}

func TestInsertMode_AltLeft_PathSkipsSlash(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("cd /tmp/my/file")
	state.Cursor.MoveTo(0, len("cd /tmp/my/file"))

	mode := NewInsertMode()
	mode.HandleKey(Key{Special: KeyLeft, Alt: true}, state)

	if state.Cursor.Pos.Col != 3 {
		t.Fatalf("Cursor col = %d, want 3", state.Cursor.Pos.Col)
	}
}

func TestInsertMode_AltRight_PathSkipsSlash(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("cd /tmp/my/file")
	state.Cursor.MoveTo(0, 3)

	mode := NewInsertMode()
	mode.HandleKey(Key{Special: KeyRight, Alt: true}, state)

	if state.Cursor.Pos.Col != len("cd /tmp/my/file") {
		t.Fatalf("Cursor col = %d, want %d", state.Cursor.Pos.Col, len("cd /tmp/my/file"))
	}
}

func TestInsertMode_ShiftAltLeft_SelectsPathAsSingleWord(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("cd /tmp/my/file")
	state.Cursor.MoveTo(0, len("cd /tmp/my/file"))

	mode := NewInsertMode()
	mode.HandleKey(Key{Special: KeyLeft, Shift: true, Alt: true}, state)

	if !state.Cursor.HasSelection() {
		t.Fatal("Shift+Alt+Left should start selection")
	}
	start, end := state.Cursor.SelectionRange()
	if start.Col != 3 || end.Col != len("cd /tmp/my/file") {
		t.Fatalf("SelectionRange = (%d,%d), want (3,%d)", start.Col, end.Col, len("cd /tmp/my/file"))
	}
}

func TestInsertMode_AltBackspace_DeletesWholePathToken(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("cd /tmp/my/file")
	state.Cursor.MoveTo(0, len("cd /tmp/my/file"))

	mode := NewInsertMode()
	result := mode.HandleKey(Key{Special: KeyBackspace, Alt: true}, state)

	if result.Action != ActionDelete {
		t.Fatalf("Action = %v, want %v", result.Action, ActionDelete)
	}
	if state.Buffer.Content() != "cd " {
		t.Fatalf("Content = %q, want %q", state.Buffer.Content(), "cd ")
	}
}

func TestInsertMode_PlainArrow_ClearsSelection(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello")
	state.Cursor.MoveTo(0, 0)

	mode := NewInsertMode()
	// Create selection
	mode.HandleKey(Key{Special: KeyRight, Shift: true}, state)
	mode.HandleKey(Key{Special: KeyRight, Shift: true}, state)
	if !state.Cursor.HasSelection() {
		t.Fatal("Should have selection")
	}

	// Plain arrow clears it
	mode.HandleKey(Key{Special: KeyRight}, state)
	if state.Cursor.HasSelection() {
		t.Fatal("Plain Right should clear selection")
	}
}

func TestInsertMode_TypeOverSelection_ReplacesText(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello")
	state.Cursor.MoveTo(0, 0)

	mode := NewInsertMode()
	// Select "hel"
	mode.HandleKey(Key{Special: KeyRight, Shift: true}, state)
	mode.HandleKey(Key{Special: KeyRight, Shift: true}, state)
	mode.HandleKey(Key{Special: KeyRight, Shift: true}, state)

	// Type 'x' to replace
	mode.HandleKey(Key{Rune: 'x'}, state)

	if state.Buffer.Content() != "xlo" {
		t.Errorf("Content = %q, want %q", state.Buffer.Content(), "xlo")
	}
	if state.Cursor.HasSelection() {
		t.Error("Selection should be cleared after typing")
	}
}

func TestInsertMode_Backspace_DeletesSelection(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello")
	state.Cursor.MoveTo(0, 0)

	mode := NewInsertMode()
	// Select "hel"
	mode.HandleKey(Key{Special: KeyRight, Shift: true}, state)
	mode.HandleKey(Key{Special: KeyRight, Shift: true}, state)
	mode.HandleKey(Key{Special: KeyRight, Shift: true}, state)

	mode.HandleKey(Key{Special: KeyBackspace}, state)

	if state.Buffer.Content() != "lo" {
		t.Errorf("Content = %q, want %q", state.Buffer.Content(), "lo")
	}
}

func TestInsertMode_Delete_DeletesSelection(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello")
	state.Cursor.MoveTo(0, 0)

	mode := NewInsertMode()
	// Select "hel"
	mode.HandleKey(Key{Special: KeyRight, Shift: true}, state)
	mode.HandleKey(Key{Special: KeyRight, Shift: true}, state)
	mode.HandleKey(Key{Special: KeyRight, Shift: true}, state)

	mode.HandleKey(Key{Special: KeyDelete}, state)

	if state.Buffer.Content() != "lo" {
		t.Errorf("Content = %q, want %q", state.Buffer.Content(), "lo")
	}
}

func TestInsertMode_Paste_ReplacesSelection(t *testing.T) {
	state := NewEditorState()
	state.LineContinuation = false
	state.Buffer = NewBufferFromString("hello")
	state.Cursor.MoveTo(0, 0)

	mode := NewInsertMode()
	// Select "hel"
	mode.HandleKey(Key{Special: KeyRight, Shift: true}, state)
	mode.HandleKey(Key{Special: KeyRight, Shift: true}, state)
	mode.HandleKey(Key{Special: KeyRight, Shift: true}, state)

	mode.HandleKey(Key{Special: KeyPaste, PasteText: "xyz"}, state)

	if state.Buffer.Content() != "xyzlo" {
		t.Errorf("Content = %q, want %q", state.Buffer.Content(), "xyzlo")
	}
}

func TestInsertMode_CtrlA_ClearsSelection(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello")
	state.Cursor.MoveTo(0, 0)

	mode := NewInsertMode()
	// Create selection
	mode.HandleKey(Key{Special: KeyRight, Shift: true}, state)
	mode.HandleKey(Key{Special: KeyRight, Shift: true}, state)

	// Ctrl+A clears selection and moves to start
	mode.HandleKey(Key{Rune: 'a', Ctrl: true}, state)

	if state.Cursor.HasSelection() {
		t.Error("Ctrl+A should clear selection")
	}
	if state.Cursor.Pos.Col != 0 {
		t.Errorf("Cursor col = %d, want 0", state.Cursor.Pos.Col)
	}
}

func TestInsertMode_HistoryNav_ClearsSelection(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("hello")
	state.Cursor.MoveTo(0, 2)

	mode := NewInsertMode()
	// Create selection
	state.Cursor.StartSelection()
	state.Cursor.MoveTo(0, 4)

	// Up arrow at row 0 triggers history and clears selection
	result := mode.HandleKey(Key{Special: KeyUp}, state)

	if result.HistoryPrev != true {
		t.Error("Up at row 0 should trigger HistoryPrev")
	}
	if state.Cursor.HasSelection() {
		t.Error("History navigation should clear selection")
	}
}
