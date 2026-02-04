package shell

import (
	"bytes"
	"testing"
)

func TestConversationUI_TintedLine(t *testing.T) {
	var buf bytes.Buffer
	ui := NewConversationUI(&buf, "#7c3aed") // Purple accent

	ui.WriteTintedLine("Hello world")

	output := buf.String()

	// Should contain background color escape sequence
	if !bytes.Contains([]byte(output), []byte("\x1b[48;2;")) {
		t.Error("expected 24-bit background color escape sequence")
	}

	// Should contain the text
	if !bytes.Contains([]byte(output), []byte("Hello world")) {
		t.Error("expected text content")
	}

	// Should end with reset
	if !bytes.Contains([]byte(output), []byte("\x1b[0m")) {
		t.Error("expected ANSI reset at end")
	}
}

func TestConversationUI_InputPrompt(t *testing.T) {
	var buf bytes.Buffer
	ui := NewConversationUI(&buf, "#7c3aed")

	ui.WriteInputPrompt()

	output := buf.String()

	// Should contain double bar character
	if !bytes.Contains([]byte(output), []byte("║")) {
		t.Error("expected ║ input prompt character")
	}
}

func TestConversationUI_Hints(t *testing.T) {
	var buf bytes.Buffer
	ui := NewConversationUI(&buf, "#7c3aed")

	ui.WriteHints()

	output := buf.String()

	// Should contain hint text
	if !bytes.Contains([]byte(output), []byte("Esc exit")) {
		t.Error("expected 'Esc exit' in hints")
	}
	if !bytes.Contains([]byte(output), []byte("!cmd shell")) {
		t.Error("expected '!cmd shell' in hints")
	}
}

func TestConversationUI_ClearTint(t *testing.T) {
	var buf bytes.Buffer
	ui := NewConversationUI(&buf, "#7c3aed")

	// Write some tinted lines
	ui.WriteTintedLine("Line 1")
	ui.WriteTintedLine("Line 2")

	buf.Reset()

	// Clear should output lines without tint
	ui.ClearTint()

	// After clearing, new writes should not have background
	// (This is a state test - the UI remembers it's cleared)
}
