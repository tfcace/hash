// internal/editor/editor_test.go
package editor

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

func TestEditor_SimpleInput(t *testing.T) {
	// Simulate typing "hello" and pressing Enter (in normal mode)
	input := bytes.NewReader([]byte{
		'h', 'e', 'l', 'l', 'o', // Type hello
		0x1b, // Escape to normal mode
		'\r', // Enter to submit
	})
	var output bytes.Buffer

	cfg := Config{
		Keybindings: "helix",
	}
	ed := New(cfg, input, &output)

	result, err := ed.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Text != "hello" {
		t.Errorf("Text = %q, want %q", result.Text, "hello")
	}
	if result.Canceled {
		t.Error("Canceled = true, want false")
	}
}

func TestEditor_EnterSubmitsInInsertMode(t *testing.T) {
	// Type "hello" and press Enter - should submit directly from insert mode
	input := bytes.NewReader([]byte{
		'h', 'e', 'l', 'l', 'o',
		'\r', // Enter in insert mode = submit
	})
	var output bytes.Buffer

	cfg := Config{Keybindings: "helix"}
	ed := New(cfg, input, &output)

	result, err := ed.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Text != "hello" {
		t.Errorf("Text = %q, want %q", result.Text, "hello")
	}
}

func TestEditor_UTF8HebrewInputRoundTrips(t *testing.T) {
	input := bytes.NewReader(append([]byte("שלום"), '\r'))
	var output bytes.Buffer

	ed := New(Config{Keybindings: "helix"}, input, &output)

	result, err := ed.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Text != "שלום" {
		t.Fatalf("Text = %q, want %q", result.Text, "שלום")
	}
}

func TestEditor_EscapeCancelsWhenConfigured(t *testing.T) {
	input := bytes.NewReader([]byte{
		0x1b, // Escape
		'\r', // Would submit if Escape only switched modes
	})
	var output bytes.Buffer

	cfg := Config{
		Keybindings:    "helix",
		CancelOnEscape: true,
	}
	ed := New(cfg, input, &output)

	result, err := ed.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.Canceled {
		t.Fatalf("Escape should cancel when configured, got result %#v", result)
	}
}

func TestEditor_HistoryCallback(t *testing.T) {
	// Up arrow on first line should trigger history callback
	input := bytes.NewReader([]byte{
		0x1b, '[', 'A', // Up arrow
		0x1b, // Escape
		'\r', // Submit
	})
	var output bytes.Buffer

	historyCalled := false
	cfg := Config{
		Keybindings: "helix",
		HistoryFunc: func(dir int, currentLine string) string {
			historyCalled = true
			if dir == -1 {
				return "previous command"
			}
			return ""
		},
	}
	ed := New(cfg, input, &output)

	result, _ := ed.Run(context.Background())

	if !historyCalled {
		t.Error("HistoryFunc should have been called")
	}
	if result.Text != "previous command" {
		t.Errorf("Text = %q, want %q", result.Text, "previous command")
	}
}

func TestEditor_CompletionState(t *testing.T) {
	cfg := Config{
		CompleteFunc: func(line string, pos int) []Completion {
			return []Completion{
				{Text: "foo", Description: "Foo item"},
				{Text: "foobar", Description: "Foobar item"},
			}
		},
	}
	ed := New(cfg, strings.NewReader(""), io.Discard)

	// Initially no completion active
	if ed.completionActive {
		t.Error("completion should not be active initially")
	}
}

func TestEditor_TriggerCompletion(t *testing.T) {
	completeCalled := false
	cfg := Config{
		CompleteFunc: func(line string, pos int) []Completion {
			completeCalled = true
			// Return multiple items so menu activates
			return []Completion{{Text: "test1"}, {Text: "test2"}}
		},
	}
	ed := New(cfg, strings.NewReader(""), io.Discard)
	ed.state.Buffer = NewBufferFromString("foo")
	ed.state.Cursor.MoveTo(0, 3)

	ed.triggerCompletion()

	if !completeCalled {
		t.Error("CompleteFunc should have been called")
	}
	if !ed.completionActive {
		t.Error("completion should be active after triggering")
	}
}

func TestEditor_TriggerCompletion_NoFunc(t *testing.T) {
	cfg := Config{
		CompleteFunc: nil, // No completion function
	}
	ed := New(cfg, strings.NewReader(""), io.Discard)
	ed.state.Buffer = NewBufferFromString("foo")
	ed.state.Cursor.MoveTo(0, 3)

	// Should not panic
	ed.triggerCompletion()

	if ed.completionActive {
		t.Error("completion should not be active without CompleteFunc")
	}
}

func TestEditor_TriggerCompletion_NoMatches(t *testing.T) {
	cfg := Config{
		CompleteFunc: func(line string, pos int) []Completion {
			return []Completion{} // Empty results
		},
	}
	ed := New(cfg, strings.NewReader(""), io.Discard)
	ed.state.Buffer = NewBufferFromString("xyz")
	ed.state.Cursor.MoveTo(0, 3)

	ed.triggerCompletion()

	if ed.completionActive {
		t.Error("completion should not be active with empty results")
	}
}

func TestEditor_TriggerCompletion_SingleMatch(t *testing.T) {
	cfg := Config{
		CompleteFunc: func(line string, pos int) []Completion {
			// Single match should auto-accept
			return []Completion{{Text: "foobar"}}
		},
	}
	ed := New(cfg, strings.NewReader(""), io.Discard)
	ed.state.Buffer = NewBufferFromString("foo")
	ed.state.Cursor.MoveTo(0, 3)

	ed.triggerCompletion()

	// Single match auto-accepts, so menu should NOT be active
	if ed.completionActive {
		t.Error("completion should not be active after single-match auto-accept")
	}
	// Buffer should contain the completion
	if ed.state.Buffer.Content() != "foobar" {
		t.Errorf("Buffer = %q, want %q", ed.state.Buffer.Content(), "foobar")
	}
}

func TestEditor_CursorOffset(t *testing.T) {
	ed := New(Config{}, strings.NewReader(""), io.Discard)
	ed.state.Buffer = NewBufferFromString("hello\nworld")

	// Cursor at start
	ed.state.Cursor.MoveTo(0, 0)
	if offset := ed.cursorOffset(); offset != 0 {
		t.Errorf("cursorOffset() = %d, want 0", offset)
	}

	// Cursor at end of first line
	ed.state.Cursor.MoveTo(0, 5)
	if offset := ed.cursorOffset(); offset != 5 {
		t.Errorf("cursorOffset() = %d, want 5", offset)
	}

	// Cursor at start of second line
	ed.state.Cursor.MoveTo(1, 0)
	if offset := ed.cursorOffset(); offset != 6 { // 5 + 1 (newline)
		t.Errorf("cursorOffset() = %d, want 6", offset)
	}

	// Cursor in middle of second line
	ed.state.Cursor.MoveTo(1, 3)
	if offset := ed.cursorOffset(); offset != 9 { // 5 + 1 + 3
		t.Errorf("cursorOffset() = %d, want 9", offset)
	}
}

func TestEditor_FindWordStart(t *testing.T) {
	ed := New(Config{}, strings.NewReader(""), io.Discard)

	tests := []struct {
		line string
		col  int
		want int
	}{
		{"hello", 5, 0},       // At end of word
		{"hello world", 8, 6}, // In middle of second word
		{"hello world", 6, 6}, // At start of second word
		{"hello world", 5, 0}, // At space
		{"", 0, 0},            // Empty line
		{"  hello", 7, 2},     // Word after spaces
		{"foo\tbar", 7, 4},    // Tab separator
	}

	for _, tt := range tests {
		ed.state.Buffer = NewBufferFromString(tt.line)
		ed.state.Cursor.MoveTo(0, tt.col)
		got := ed.findWordStart()
		if got != tt.want {
			t.Errorf("findWordStart(%q, col=%d) = %d, want %d", tt.line, tt.col, got, tt.want)
		}
	}
}

func TestEditor_TriggerCompletion_MultilineBuffer(t *testing.T) {
	// This test ensures completion works correctly with multiline buffers.
	// Previously, the code sliced the full buffer content using a column index
	// that was relative to the current line, which caused incorrect results or panics.
	cfg := Config{
		CompleteFunc: func(line string, pos int) []Completion {
			// Return multiple items so menu activates
			return []Completion{{Text: "bar"}, {Text: "baz"}}
		},
	}
	ed := New(cfg, strings.NewReader(""), io.Discard)

	// Set up a multiline buffer with cursor on the second line
	ed.state.Buffer = NewBufferFromString("first line\nfoo b")
	ed.state.Cursor.MoveTo(1, 5) // Cursor after "foo b" on line 2

	// This should not panic and should correctly extract "b" as the prefix
	ed.triggerCompletion()

	if !ed.completionActive {
		t.Error("completion should be active after triggering")
	}
	// The prefix should be "b" (just the word on current line, not involving previous lines)
	if ed.completionPrefix != "b" {
		t.Errorf("completionPrefix = %q, want %q", ed.completionPrefix, "b")
	}
	// completionCol should be 4 (where "b" starts on line 2)
	if ed.completionCol != 4 {
		t.Errorf("completionCol = %d, want 4", ed.completionCol)
	}
}

func TestEditor_AcceptCompletion(t *testing.T) {
	cfg := Config{}
	ed := New(cfg, strings.NewReader(""), io.Discard)
	ed.state.Buffer = NewBufferFromString("fo")
	ed.state.Cursor.MoveTo(0, 2)
	ed.completionCol = 0

	ed.acceptCompletion(Completion{Text: "foobar"})

	content := ed.state.Buffer.Content()
	if content != "foobar" {
		t.Errorf("Buffer = %q, want %q", content, "foobar")
	}
	if ed.state.Cursor.Pos.Col != 6 {
		t.Errorf("Cursor col = %d, want 6", ed.state.Cursor.Pos.Col)
	}
	if ed.completionActive {
		t.Error("completion should be dismissed after accept")
	}
}

func TestEditor_GhostModifiedTabAcceptsWord(t *testing.T) {
	ed := New(Config{}, strings.NewReader(""), io.Discard)
	ed.state.Buffer = NewBufferFromString("git ")
	ed.state.Cursor.MoveTo(0, 4)
	ed.ghost.Set("commit -m test")
	ed.ghost.FromAgent = true

	handled := ed.handleGhostTextKey(Key{Special: KeyTab, Alt: true})
	if !handled {
		t.Fatal("expected Alt+Tab to be handled")
	}
	if got := ed.state.Buffer.Content(); got != "git commit " {
		t.Fatalf("buffer = %q, want %q", got, "git commit ")
	}
	if got := ed.ghost.Remaining(); got != "-m test" {
		t.Fatalf("remaining ghost = %q, want %q", got, "-m test")
	}
}

func TestEditor_FindWordStartShellAware(t *testing.T) {
	tests := []struct {
		name string
		line string
		want int
	}{
		{name: "plain", line: "cp file", want: 3},
		{name: "escaped space", line: `cp My\ File.txt`, want: 3},
		{name: "single quoted space", line: `cp 'My File.txt`, want: 3},
		{name: "double quoted space", line: `cp "My File.txt`, want: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ed := New(Config{}, strings.NewReader(""), &strings.Builder{})
			ed.state.Buffer = NewBufferFromString(tt.line)
			ed.state.Cursor.MoveTo(0, len(tt.line))
			if got := ed.findWordStart(); got != tt.want {
				t.Fatalf("findWordStart() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestEditor_CompletionNavigation(t *testing.T) {
	cfg := Config{}
	ed := New(cfg, strings.NewReader(""), io.Discard)
	ed.completionItems = []Completion{
		{Text: "foo"},
		{Text: "bar"},
		{Text: "baz"},
	}
	ed.completionActive = true
	ed.completionIndex = 0

	// Down moves to next
	ed.handleCompletionKey(Key{Special: KeyDown})
	if ed.completionIndex != 1 {
		t.Errorf("After down: index = %d, want 1", ed.completionIndex)
	}

	// Down again
	ed.handleCompletionKey(Key{Special: KeyDown})
	if ed.completionIndex != 2 {
		t.Errorf("After down 2: index = %d, want 2", ed.completionIndex)
	}

	// Down wraps
	ed.handleCompletionKey(Key{Special: KeyDown})
	if ed.completionIndex != 0 {
		t.Errorf("After wrap: index = %d, want 0", ed.completionIndex)
	}

	// Up wraps backwards
	ed.handleCompletionKey(Key{Special: KeyUp})
	if ed.completionIndex != 2 {
		t.Errorf("After up wrap: index = %d, want 2", ed.completionIndex)
	}
}

func TestEditor_CompletionKey_EmptyItems(t *testing.T) {
	cfg := Config{}
	ed := New(cfg, strings.NewReader(""), io.Discard)
	ed.completionItems = nil // Empty items
	ed.completionActive = true

	// Should not panic and should dismiss completion
	handled := ed.handleCompletionKey(Key{Special: KeyDown})
	if handled {
		t.Error("handleCompletionKey should return false for empty items")
	}
	if ed.completionActive {
		t.Error("completion should be dismissed when items are empty")
	}

	// Test with KeyTab as well (also uses modulo)
	ed.completionActive = true
	ed.completionItems = []Completion{} // Explicitly empty slice
	handled = ed.handleCompletionKey(Key{Special: KeyTab})
	if handled {
		t.Error("handleCompletionKey should return false for empty slice")
	}

	// Test with KeyEnter (would cause index out of bounds)
	ed.completionActive = true
	ed.completionItems = nil
	handled = ed.handleCompletionKey(Key{Special: KeyEnter})
	if handled {
		t.Error("handleCompletionKey should return false for empty items on Enter")
	}
}
