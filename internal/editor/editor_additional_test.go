package editor

import (
	"strings"
	"testing"
)

// TestEndsWithBackslash_Comprehensive tests all backslash detection cases.
func TestEndsWithBackslash_Comprehensive(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"empty", "", false},
		{"no backslash", "hello", false},
		{"ends with backslash", "hello\\", true},
		{"backslash with trailing space", "hello\\ ", true},
		{"backslash with trailing tab", "hello\\\t", true},
		{"backslash with multiple trailing spaces", "hello\\   ", true},
		{"backslash in middle", "hel\\lo", false},
		{"just backslash", "\\", true},
		{"just spaces", "   ", false},
		{"backslash not at end", "\\hello", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := endsWithBackslash(tt.input)
			if got != tt.want {
				t.Errorf("endsWithBackslash(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestSplitLines_AllLineEndings tests all line ending types.
func TestSplitLines_AllLineEndings(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty", "", []string{""}},
		{"single line", "hello", []string{"hello"}},
		{"two lines LF", "hello\nworld", []string{"hello", "world"}},
		{"two lines CRLF", "hello\r\nworld", []string{"hello", "world"}},
		{"two lines CR", "hello\rworld", []string{"hello", "world"}},
		{"empty lines", "a\n\nb", []string{"a", "", "b"}},
		{"trailing newline", "hello\n", []string{"hello", ""}},
		{"multiple trailing", "a\n\n", []string{"a", "", ""}},
		{"mixed line endings", "a\nb\r\nc\rd", []string{"a", "b", "c", "d"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitLines(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("splitLines(%q) = %v (len %d), want %v (len %d)",
					tt.input, got, len(got), tt.want, len(tt.want))
				return
			}
			for i, line := range got {
				if line != tt.want[i] {
					t.Errorf("splitLines(%q)[%d] = %q, want %q", tt.input, i, line, tt.want[i])
				}
			}
		})
	}
}

// TestAddLineContinuations_AllCases tests all line continuation cases.
func TestAddLineContinuations_AllCases(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"single line", "hello", "hello"},
		{"two lines no backslash", "hello\nworld", "hello \\\nworld"},
		{"already has backslash", "hello\\\nworld", "hello\\\nworld"},
		{"backslash with space", "hello\\ \nworld", "hello\\ \nworld"},
		{"multiple lines", "a\nb\nc", "a \\\nb \\\nc"},
		{"empty lines", "a\n\nb", "a \\\n \\\nb"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := addLineContinuations(tt.input)
			if got != tt.want {
				t.Errorf("addLineContinuations(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestBuffer_Line_OutOfBounds tests buffer line access with invalid indices.
func TestBuffer_Line_OutOfBounds(t *testing.T) {
	buf := NewBufferFromString("hello\nworld")

	// Negative index should return empty
	line := buf.Line(-1)
	if line != "" {
		t.Errorf("Line(-1) = %q, want empty", line)
	}

	// Index beyond end should return empty
	line = buf.Line(10)
	if line != "" {
		t.Errorf("Line(10) = %q, want empty", line)
	}
}

// TestBuffer_Insert_OutOfBounds tests insert with out of bounds column.
func TestBuffer_Insert_OutOfBounds(t *testing.T) {
	buf := NewBufferFromString("hello")

	// Insert at column beyond line length should append at end
	buf.Insert(0, 100, " world")
	if buf.Content() != "hello world" {
		t.Errorf("Content() = %q, want %q", buf.Content(), "hello world")
	}
}

// TestBuffer_Delete_NormalizedRange tests delete with reversed range.
func TestBuffer_Delete_NormalizedRange(t *testing.T) {
	buf := NewBufferFromString("hello world")

	// Delete with end before start should still work (normalized)
	buf.Delete(Position{0, 11}, Position{0, 6})
	if buf.Content() != "hello " {
		t.Errorf("Content() = %q, want %q", buf.Content(), "hello ")
	}
}

// TestBuffer_ReplaceLine tests the ReplaceLine method.
func TestBuffer_ReplaceLine(t *testing.T) {
	buf := NewBufferFromString("hello\nworld")

	buf.ReplaceLine(0, "goodbye")
	if buf.Line(0) != "goodbye" {
		t.Errorf("Line(0) = %q, want %q", buf.Line(0), "goodbye")
	}
	if buf.Line(1) != "world" {
		t.Errorf("Line(1) = %q, should be unchanged", buf.Line(1))
	}

	// Out of bounds should be safe
	buf.ReplaceLine(-1, "nope")
	buf.ReplaceLine(100, "nope")
	if buf.LineCount() != 2 {
		t.Errorf("LineCount() = %d, should still be 2", buf.LineCount())
	}
}

// TestCursor_Clamp_EmptyBuffer tests clamping to empty buffer.
func TestCursor_Clamp_EmptyBuffer(t *testing.T) {
	buf := NewBuffer()
	c := NewCursor()
	c.MoveTo(5, 10)
	c.Clamp(buf)

	if c.Pos.Row != 0 {
		t.Errorf("Row = %d, want 0", c.Pos.Row)
	}
	if c.Pos.Col != 0 {
		t.Errorf("Col = %d, want 0", c.Pos.Col)
	}
}

// TestCursor_Clone tests cursor cloning.
func TestCursor_Clone(t *testing.T) {
	c := NewCursor()
	c.MoveTo(5, 10)
	c.StartSelection()

	clone := c.Clone()
	clone.MoveTo(0, 0)
	clone.ClearSelection()

	// Original should be unchanged
	if c.Pos.Row != 5 || c.Pos.Col != 10 {
		t.Errorf("Original pos changed: %+v", c.Pos)
	}
	if !c.HasSelection() {
		t.Error("Original selection should still exist")
	}
}

// TestUndoStack_Empty_SafeOperations tests safe operations on empty stack.
func TestUndoStack_Empty_SafeOperations(t *testing.T) {
	u := NewUndoStack()

	if u.CanUndo() {
		t.Error("CanUndo() should be false on empty stack")
	}
	if u.CanRedo() {
		t.Error("CanRedo() should be false on empty stack")
	}

	// Should not panic
	buf, cur := u.Undo()
	if buf != nil || cur != nil {
		t.Error("Undo() on empty stack should return nil")
	}
	buf, cur = u.Redo()
	if buf != nil || cur != nil {
		t.Error("Redo() on empty stack should return nil")
	}
}

// TestUndoStack_PushAndUndo_StateRecovery tests state recovery through undo.
func TestUndoStack_PushAndUndo_StateRecovery(t *testing.T) {
	u := NewUndoStack()
	buf1 := NewBufferFromString("hello")
	cur := NewCursor()

	// First push - sets initial state
	u.Push(buf1, cur)

	// Can't undo after first push (no previous state)
	if u.CanUndo() {
		t.Error("CanUndo() should be false after first push (no previous state)")
	}

	// Create second state
	buf2 := NewBufferFromString("hello world")
	u.Push(buf2, cur)

	// Now we can undo
	if !u.CanUndo() {
		t.Error("CanUndo() should be true after second push")
	}

	// Undo should return previous state
	oldBuf, _ := u.Undo()
	if oldBuf == nil {
		t.Fatal("Undo() returned nil buffer")
	}
	if oldBuf.Content() != "hello" {
		t.Errorf("Undo buffer content = %q, want %q", oldBuf.Content(), "hello")
	}
}

// TestUndoStack_RedoTruncation tests that new push truncates redo history.
func TestUndoStack_RedoTruncation(t *testing.T) {
	u := NewUndoStack()
	buf := NewBufferFromString("a")
	cur := NewCursor()

	u.Push(buf, cur)
	buf = NewBufferFromString("ab")
	u.Push(buf, cur)
	buf = NewBufferFromString("abc")
	u.Push(buf, cur)

	// Undo twice
	u.Undo()
	u.Undo()

	// Should have redo available
	if !u.CanRedo() {
		t.Error("CanRedo() should be true after undo")
	}

	// Push new state - should truncate redo history
	buf = NewBufferFromString("new")
	u.Push(buf, cur)

	if u.CanRedo() {
		t.Error("CanRedo() should be false after new push")
	}
}

// TestGhostText_AcceptWord_EdgeCases tests word acceptance edge cases.
func TestGhostText_AcceptWord_EdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		calls int
		want  []string
	}{
		{"empty", "", 1, []string{""}},
		{"single word", "hello", 1, []string{"hello"}},
		{"leading spaces", "  hello", 1, []string{"  hello"}},
		{"multiple spaces", "hello   world", 2, []string{"hello   ", "world"}},
		{"tabs", "hello\tworld", 2, []string{"hello\t", "world"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGhostText()
			g.Set(tt.text)

			for i := 0; i < tt.calls && i < len(tt.want); i++ {
				got := g.AcceptWord()
				if got != tt.want[i] {
					t.Errorf("AcceptWord() call %d = %q, want %q", i+1, got, tt.want[i])
				}
			}
		})
	}
}

// TestBuffer_MultilineInsert tests inserting text with multiple newlines.
func TestBuffer_MultilineInsert(t *testing.T) {
	buf := NewBuffer()
	buf.Insert(0, 0, "line1\nline2\nline3")

	if buf.LineCount() != 3 {
		t.Errorf("LineCount() = %d, want 3", buf.LineCount())
	}
	if buf.Line(0) != "line1" {
		t.Errorf("Line(0) = %q, want %q", buf.Line(0), "line1")
	}
	if buf.Line(2) != "line3" {
		t.Errorf("Line(2) = %q, want %q", buf.Line(2), "line3")
	}
}

// TestBuffer_DeleteToJoinLines tests deleting across line boundary.
func TestBuffer_DeleteToJoinLines(t *testing.T) {
	buf := NewBufferFromString("hello\nworld")

	// Delete from end of first line to start of second (join lines)
	buf.Delete(Position{0, 5}, Position{1, 0})

	if buf.LineCount() != 1 {
		t.Errorf("LineCount() = %d, want 1", buf.LineCount())
	}
	if buf.Content() != "helloworld" {
		t.Errorf("Content() = %q, want %q", buf.Content(), "helloworld")
	}
}

// TestNewBufferFromString_EmptyLines tests buffer creation with empty lines.
func TestNewBufferFromString_EmptyLines(t *testing.T) {
	buf := NewBufferFromString("a\n\nb")

	if buf.LineCount() != 3 {
		t.Errorf("LineCount() = %d, want 3", buf.LineCount())
	}
	if buf.Line(1) != "" {
		t.Errorf("Line(1) = %q, want empty", buf.Line(1))
	}
}

// TestNewBufferFromString_TrailingNewline tests buffer with trailing newline.
func TestNewBufferFromString_TrailingNewline(t *testing.T) {
	buf := NewBufferFromString("hello\n")

	if buf.LineCount() != 2 {
		t.Errorf("LineCount() = %d, want 2", buf.LineCount())
	}
	if buf.Line(0) != "hello" {
		t.Errorf("Line(0) = %q, want %q", buf.Line(0), "hello")
	}
	if buf.Line(1) != "" {
		t.Errorf("Line(1) = %q, want empty", buf.Line(1))
	}
}

// TestCursor_SelectionRange_MultiLine tests selection across multiple lines.
func TestCursor_SelectionRange_MultiLine(t *testing.T) {
	c := NewCursor()
	c.MoveTo(0, 5)
	c.StartSelection()
	c.MoveTo(2, 3)

	start, end := c.SelectionRange()
	if start.Row != 0 || start.Col != 5 {
		t.Errorf("start = %+v, want {0, 5}", start)
	}
	if end.Row != 2 || end.Col != 3 {
		t.Errorf("end = %+v, want {2, 3}", end)
	}
}

// TestCursor_SelectionRange_MultiLineReversed tests reversed multi-line selection.
func TestCursor_SelectionRange_MultiLineReversed(t *testing.T) {
	c := NewCursor()
	c.MoveTo(2, 3)
	c.StartSelection()
	c.MoveTo(0, 5)

	start, end := c.SelectionRange()
	// Should be normalized
	if start.Row != 0 || start.Col != 5 {
		t.Errorf("start = %+v, want {0, 5}", start)
	}
	if end.Row != 2 || end.Col != 3 {
		t.Errorf("end = %+v, want {2, 3}", end)
	}
}

// TestGhostText_FromAgent tests the FromAgent flag.
func TestGhostText_FromAgent(t *testing.T) {
	g := NewGhostText()
	g.Set("suggestion")
	g.FromAgent = true

	if !g.FromAgent {
		t.Error("FromAgent should be true")
	}

	g.Clear()
	if g.FromAgent {
		t.Error("FromAgent should be false after Clear")
	}
}

// TestBuffer_Content_LongBuffer tests content retrieval with many lines.
func TestBuffer_Content_LongBuffer(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "line"
	}
	content := strings.Join(lines, "\n")
	buf := NewBufferFromString(content)

	if buf.LineCount() != 100 {
		t.Errorf("LineCount() = %d, want 100", buf.LineCount())
	}

	got := buf.Content()
	if got != content {
		t.Errorf("Content() length = %d, want %d", len(got), len(content))
	}
}
