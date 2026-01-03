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

	buf := NewBufferFromString("hello")
	cur := NewCursor()

	d.Render(buf, cur, false)

	output := out.String()
	if !strings.Contains(output, "│") {
		t.Errorf("Output should contain gutter, got %q", output)
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
