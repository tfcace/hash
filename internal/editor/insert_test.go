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
		{"hello \\ ", true},   // Trailing spaces don't matter
		{"hello \\  ", true},  // Multiple trailing spaces
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
		{"hello\r\nworld", []string{"hello", "world"}},  // Windows line endings
		{"hello\rworld", []string{"hello", "world"}},    // Old Mac line endings
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
		{"hello", "hello"},                                      // Single line unchanged
		{"hello\nworld", "hello \\\nworld"},                     // Two lines
		{"a\nb\nc", "a \\\nb \\\nc"},                            // Three lines
		{"hello \\\nworld", "hello \\\nworld"},                  // Already has continuation
		{"hello\\\nworld", "hello\\\nworld"},                    // Continuation without space
		{"", ""},                                                // Empty string
		{"hello\n", "hello \\\n"},                               // Trailing newline
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
