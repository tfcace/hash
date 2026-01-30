// internal/editor/integration_test.go
package editor

import (
	"bytes"
	"context"
	"testing"
)

// Note: These tests use bytes.Reader which sends all bytes immediately.
// In a real terminal, ESC followed by a letter would have a time delay,
// allowing the InputReader to distinguish bare ESC from Alt+key sequences.
// For testing normal mode commands, we need to send the command bytes
// separately from the ESC byte by using control characters as separators.

func TestIntegration_TypeAndSubmit(t *testing.T) {
	// Simple: type "hello" and submit
	input := []byte{
		'h', 'e', 'l', 'l', 'o', // Type hello
		0x1b, // Escape to normal mode
		'\r', // Enter to submit
	}

	var output bytes.Buffer
	ed := New(Config{Keybindings: "helix"}, bytes.NewReader(input), &output)

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

func TestIntegration_EnterSubmitsInInsertMode(t *testing.T) {
	// Enter in insert mode should submit (not add newline)
	input := []byte{
		'h', 'e', 'l', 'l', 'o',
		'\r', // Enter in insert mode = submit
	}
	var output bytes.Buffer

	ed := New(Config{Keybindings: "helix"}, bytes.NewReader(input), &output)

	result, err := ed.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Text != "hello" {
		t.Errorf("Text = %q, want %q", result.Text, "hello")
	}
}

func TestIntegration_ShiftEnterInsertsNewlineWithContinuation(t *testing.T) {
	// Test that Shift+Enter inserts newline with shell continuation
	// (CSI u sequence parsing is tested in key_test.go)
	state := NewEditorState()
	mode := NewInsertMode()

	// Type "hello"
	for _, r := range "hello" {
		mode.HandleKey(Key{Rune: r}, state)
	}

	// Shift+Enter - should insert " \" then newline for shell continuation
	mode.HandleKey(Key{Special: KeyEnter, Shift: true}, state)

	// Type "world"
	for _, r := range "world" {
		mode.HandleKey(Key{Rune: r}, state)
	}

	// Expect shell continuation: "hello \\\nworld"
	expected := "hello \\\nworld"
	if state.Buffer.Content() != expected {
		t.Errorf("Content = %q, want %q", state.Buffer.Content(), expected)
	}
}

func TestIntegration_ShiftEnterNoDoubleContinuation(t *testing.T) {
	// Test that Shift+Enter doesn't add extra continuation if line already ends with \
	state := NewEditorState()
	mode := NewInsertMode()

	// Type "hello \"
	for _, r := range "hello \\" {
		mode.HandleKey(Key{Rune: r}, state)
	}

	// Shift+Enter - should NOT add another continuation since line ends with \
	mode.HandleKey(Key{Special: KeyEnter, Shift: true}, state)

	// Type "world"
	for _, r := range "world" {
		mode.HandleKey(Key{Rune: r}, state)
	}

	// Line already had \, so just newline added
	expected := "hello \\\nworld"
	if state.Buffer.Content() != expected {
		t.Errorf("Content = %q, want %q", state.Buffer.Content(), expected)
	}
}

func TestIntegration_HistoryNavigation(t *testing.T) {
	// Up arrow on first line should trigger history callback
	input := []byte{
		0x1b, '[', 'A', // Up arrow
		0x1b, // Escape
		'\r', // Submit
	}
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
	ed := New(cfg, bytes.NewReader(input), &output)

	result, _ := ed.Run(context.Background())

	if !historyCalled {
		t.Error("HistoryFunc should have been called")
	}
	if result.Text != "previous command" {
		t.Errorf("Text = %q, want %q", result.Text, "previous command")
	}
}

func TestIntegration_CtrlC_ClearsBuffer(t *testing.T) {
	// Type something, Ctrl+C should clear, then type new content
	input := []byte{
		'h', 'e', 'l', 'l', 'o',
		0x03, // Ctrl+C
		'w', 'o', 'r', 'l', 'd',
		0x1b, // Escape
		'\r', // Submit
	}

	var output bytes.Buffer
	ed := New(Config{Keybindings: "helix"}, bytes.NewReader(input), &output)

	result, err := ed.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// Buffer was cleared, so only "world" remains
	if result.Text != "world" {
		t.Errorf("Text = %q, want %q", result.Text, "world")
	}
}

func TestIntegration_CtrlC_EmptyBufferCancels(t *testing.T) {
	// Ctrl+C on empty buffer should cancel
	input := []byte{
		0x03, // Ctrl+C
	}

	var output bytes.Buffer
	ed := New(Config{Keybindings: "helix"}, bytes.NewReader(input), &output)

	result, _ := ed.Run(context.Background())

	if !result.Canceled {
		t.Error("Canceled = false, want true")
	}
}

func TestIntegration_CtrlD_EmptyBufferEOF(t *testing.T) {
	// Ctrl+D on empty buffer should signal EOF (exit shell)
	input := []byte{
		0x04, // Ctrl+D
	}

	var output bytes.Buffer
	ed := New(Config{Keybindings: "helix"}, bytes.NewReader(input), &output)

	result, _ := ed.Run(context.Background())

	if !result.EOF {
		t.Error("EOF = false, want true")
	}
}

func TestIntegration_Backspace(t *testing.T) {
	// Type and backspace
	input := []byte{
		'h', 'e', 'l', 'l', 'o', 'o',
		0x7f, // Backspace
		0x1b, // Escape
		'\r', // Submit
	}

	var output bytes.Buffer
	ed := New(Config{Keybindings: "helix"}, bytes.NewReader(input), &output)

	result, err := ed.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.Text != "hello" {
		t.Errorf("Text = %q, want %q", result.Text, "hello")
	}
}

func TestIntegration_CtrlA_MoveToStart(t *testing.T) {
	// Type, Ctrl+A, insert at start
	input := []byte{
		'e', 'l', 'l', 'o',
		0x01,       // Ctrl+A (move to start)
		'H',        // Type H
		0x1b, '\r', // Submit
	}

	var output bytes.Buffer
	ed := New(Config{Keybindings: "helix"}, bytes.NewReader(input), &output)

	result, err := ed.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.Text != "Hello" {
		t.Errorf("Text = %q, want %q", result.Text, "Hello")
	}
}

func TestIntegration_CtrlE_MoveToEnd(t *testing.T) {
	// Type, Ctrl+A (start), type, Ctrl+E (end), type more
	input := []byte{
		'h', 'e', 'l', 'l', 'o',
		0x01,       // Ctrl+A (move to start)
		0x05,       // Ctrl+E (move to end)
		'!',        // Type !
		0x1b, '\r', // Submit
	}

	var output bytes.Buffer
	ed := New(Config{Keybindings: "helix"}, bytes.NewReader(input), &output)

	result, err := ed.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.Text != "hello!" {
		t.Errorf("Text = %q, want %q", result.Text, "hello!")
	}
}

func TestIntegration_ArrowKeys(t *testing.T) {
	// Type, left arrow, insert
	input := []byte{
		'h', 'e', 'l', 'o',
		0x1b, '[', 'D', // Left arrow
		'l',        // Type l (now "hello")
		0x1b, '\r', // Submit
	}

	var output bytes.Buffer
	ed := New(Config{Keybindings: "helix"}, bytes.NewReader(input), &output)

	result, err := ed.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.Text != "hello" {
		t.Errorf("Text = %q, want %q", result.Text, "hello")
	}
}

func TestIntegration_CtrlW_DeleteWord(t *testing.T) {
	// Type words, Ctrl+W to delete last word
	input := []byte{
		'h', 'e', 'l', 'l', 'o', ' ', 'w', 'o', 'r', 'l', 'd',
		0x17,       // Ctrl+W (delete word)
		0x1b, '\r', // Submit
	}

	var output bytes.Buffer
	ed := New(Config{Keybindings: "helix"}, bytes.NewReader(input), &output)

	result, err := ed.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.Text != "hello " {
		t.Errorf("Text = %q, want %q", result.Text, "hello ")
	}
}

func TestIntegration_CtrlU_DeleteToStart(t *testing.T) {
	// Type, Ctrl+U to delete to start
	input := []byte{
		'h', 'e', 'l', 'l', 'o',
		0x15, // Ctrl+U (delete to start)
		'w', 'o', 'r', 'l', 'd',
		0x1b, '\r', // Submit
	}

	var output bytes.Buffer
	ed := New(Config{Keybindings: "helix"}, bytes.NewReader(input), &output)

	result, err := ed.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.Text != "world" {
		t.Errorf("Text = %q, want %q", result.Text, "world")
	}
}

func TestIntegration_BracketedPaste_SingleLine(t *testing.T) {
	// Test that single-line paste works without adding continuation
	state := NewEditorState()
	mode := NewInsertMode()

	// Simulate pasting "hello world"
	mode.HandleKey(Key{Special: KeyPaste, PasteText: "hello world"}, state)

	expected := "hello world"
	if state.Buffer.Content() != expected {
		t.Errorf("Content = %q, want %q", state.Buffer.Content(), expected)
	}
}

func TestIntegration_BracketedPaste_Multiline(t *testing.T) {
	// Test that multiline paste adds continuations
	state := NewEditorState()
	mode := NewInsertMode()

	// Simulate pasting multiline text
	mode.HandleKey(Key{Special: KeyPaste, PasteText: "echo hello\necho world"}, state)

	// Should add continuation before newline
	expected := "echo hello \\\necho world"
	if state.Buffer.Content() != expected {
		t.Errorf("Content = %q, want %q", state.Buffer.Content(), expected)
	}
}

func TestIntegration_BracketedPaste_PreserveExistingContinuation(t *testing.T) {
	// Test that paste preserves existing backslash continuations
	state := NewEditorState()
	mode := NewInsertMode()

	// Simulate pasting text that already has continuation
	mode.HandleKey(Key{Special: KeyPaste, PasteText: "echo hello \\\necho world"}, state)

	// Should not add extra continuation
	expected := "echo hello \\\necho world"
	if state.Buffer.Content() != expected {
		t.Errorf("Content = %q, want %q", state.Buffer.Content(), expected)
	}
}

func TestIntegration_BracketedPaste_MultipleLines(t *testing.T) {
	// Test paste with multiple lines
	state := NewEditorState()
	mode := NewInsertMode()

	// Simulate pasting 3 lines
	mode.HandleKey(Key{Special: KeyPaste, PasteText: "line1\nline2\nline3"}, state)

	// Should add continuation before each internal newline
	expected := "line1 \\\nline2 \\\nline3"
	if state.Buffer.Content() != expected {
		t.Errorf("Content = %q, want %q", state.Buffer.Content(), expected)
	}
}

func TestIntegration_TabInsertsTab_WhenNoCompleteFunc(t *testing.T) {
	// Without a CompleteFunc, Tab in insert mode triggers completion
	// but since there's no completion UI yet, the result.Complete flag is set
	// but nothing happens. This tests that the input still works.
	input := []byte{
		'h', 'e', 'l', 'l', 'o',
		'\t',       // Tab triggers Complete (but no UI)
		0x1b, '\r', // Submit
	}

	var output bytes.Buffer
	ed := New(Config{Keybindings: "helix"}, bytes.NewReader(input), &output)

	result, err := ed.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// Tab should not insert anything (Complete is triggered instead)
	if result.Text != "hello" {
		t.Errorf("Text = %q, want %q", result.Text, "hello")
	}
}
