// internal/editor/display_test.go
package editor

import (
	"bytes"
	"strings"
	"testing"
)

func TestDisplay_Render_SingleLine(t *testing.T) {
	var out bytes.Buffer
	d := NewDisplay(&out, 80, 24)

	buf := NewBufferFromString("hello")
	cur := NewCursor()
	cur.MoveTo(0, 5)

	d.Render(buf, cur, false)

	output := out.String()
	if !strings.Contains(output, "hello") {
		t.Errorf("Output should contain 'hello', got %q", output)
	}
}

func TestDisplay_Render_Multiline(t *testing.T) {
	var out bytes.Buffer
	d := NewDisplay(&out, 80, 24)

	buf := NewBufferFromString("hello\nworld")
	cur := NewCursor()

	d.Render(buf, cur, false)

	output := out.String()
	if !strings.Contains(output, "hello") || !strings.Contains(output, "world") {
		t.Errorf("Output should contain both lines, got %q", output)
	}
}

func TestDisplay_Render_WithGutter(t *testing.T) {
	var out bytes.Buffer
	d := NewDisplay(&out, 80, 24)
	d.SetGutter(true)
	d.SetMode("insert")

	buf := NewBufferFromString("hello")
	cur := NewCursor()

	d.Render(buf, cur, false)

	output := out.String()
	// Gutter shows "i│" with dim styling for insert mode
	if !strings.Contains(output, "i│") {
		t.Errorf("Output should contain mode indicator 'i│', got %q", output)
	}
	// Insert mode uses dim styling (\x1b[2m)
	if !strings.Contains(output, "\x1b[2m") {
		t.Errorf("Output should contain dim styling for insert mode, got %q", output)
	}
}

func TestDisplay_Render_WithGutter_NormalMode(t *testing.T) {
	var out bytes.Buffer
	d := NewDisplay(&out, 80, 24)
	d.SetGutter(true)
	d.SetMode("normal")

	buf := NewBufferFromString("hello")
	cur := NewCursor()

	d.Render(buf, cur, false)

	output := out.String()
	// Gutter shows "n│" with bold yellow styling for normal mode
	if !strings.Contains(output, "n│") {
		t.Errorf("Output should contain mode indicator 'n│', got %q", output)
	}
	// Normal mode uses bold yellow styling (\x1b[33;1m)
	if !strings.Contains(output, "\x1b[33;1m") {
		t.Errorf("Output should contain bold yellow styling for normal mode, got %q", output)
	}
}

func TestDisplay_CursorPosition(t *testing.T) {
	var out bytes.Buffer
	d := NewDisplay(&out, 80, 24)

	buf := NewBufferFromString("hello")
	cur := NewCursor()
	cur.MoveTo(0, 3)

	d.Render(buf, cur, false)

	// Should contain cursor positioning escape sequence
	// CSI <row>;<col>H
	output := out.String()
	if !strings.Contains(output, "\x1b[") {
		t.Errorf("Output should contain ANSI sequences")
	}
}

func TestDisplay_RenderCompletionMenu(t *testing.T) {
	var buf bytes.Buffer
	d := NewDisplay(&buf, 80, 24)

	items := []CompletionItem{
		{Text: "foo", Description: "Foo item"},
		{Text: "bar", Description: "Bar item"},
	}

	d.RenderCompletionMenu(items, 0, 5) // selected=0, startCol=5

	output := buf.String()
	if !strings.Contains(output, "foo") {
		t.Error("Menu should contain 'foo'")
	}
	if !strings.Contains(output, "bar") {
		t.Error("Menu should contain 'bar'")
	}
}

func TestDisplay_RenderCompletionMenu_WithGutter(t *testing.T) {
	var buf bytes.Buffer
	d := NewDisplay(&buf, 80, 24)
	d.SetGutter(true)
	d.SetPrompt("$ ")

	items := []CompletionItem{
		{Text: "file.txt", Description: "A file"},
	}

	// startCol=3 (e.g., after "ls " in buffer)
	// With gutter (2) + prompt "$ " (2) = prefix of 4
	// Menu should be positioned at column 3 + 4 = 7
	d.RenderCompletionMenu(items, 0, 3)

	output := buf.String()

	// The output should contain cursor forward command with the correct offset
	// \x1b[7C means move cursor forward 7 columns (3 for startCol + 4 for prefix)
	if !strings.Contains(output, "\x1b[7C") {
		t.Errorf("Menu should be positioned at column 7 (startCol 3 + prefix 4), got %q", output)
	}

	// Should use save/restore cursor to preserve cursor position
	if !strings.Contains(output, "\x1b[s") {
		t.Error("Menu should save cursor position at start")
	}
	if !strings.Contains(output, "\x1b[u") {
		t.Error("Menu should restore cursor position at end")
	}
}
