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

	d.RenderCompletionMenu(items, 0, 5, 0, 5) // selected=0, startCol=5

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
	d.RenderCompletionMenu(items, 0, 3, 0, 3)

	output := buf.String()

	// The output should contain cursor forward command with the correct offset
	// \x1b[7C means move cursor forward 7 columns (3 for startCol + 4 for prefix)
	if !strings.Contains(output, "\x1b[7C") {
		t.Errorf("Menu should be positioned at column 7 (startCol 3 + prefix 4), got %q", output)
	}

	// Should move back up one menu row and restore input cursor column.
	if !strings.Contains(output, "\x1b[1A") {
		t.Error("Menu should move back up after rendering")
	}
}

func TestDisplay_RenderCompletionMenu_ShowsColoredRailForShortLists(t *testing.T) {
	var buf bytes.Buffer
	d := NewDisplay(&buf, 80, 24)
	d.SetScrollbarColor("#06B6D4")

	items := []CompletionItem{
		{Text: "alpha", Description: "First"},
		{Text: "beta", Description: "Second"},
	}

	d.RenderCompletionMenu(items, 0, 0, 0, 0)

	output := buf.String()
	if !strings.Contains(output, "▌") {
		t.Fatalf("completion rail should render for short lists when color is configured, got %q", output)
	}
}

func TestDisplay_RenderWithFrame_FillsLineBgToEOL(t *testing.T) {
	var out bytes.Buffer
	d := NewDisplay(&out, 20, 24)
	d.SetFrame(&InputFrame{
		Prefix:      "P ",
		PrefixWidth: 2,
		LineBg:      "\x1b[48;2;1;2;3m",
	})

	buf := NewBufferFromString("hello")
	cur := NewCursor()
	cur.MoveTo(0, 5)

	d.Render(buf, cur, false)

	output := out.String()
	if !strings.Contains(output, "P hello") {
		t.Fatalf("output should contain framed line content, got %q", output)
	}
	if !strings.Contains(output, "\x1b[48;2;1;2;3m\x1b[K\x1b[0m") {
		t.Fatalf("output should fill frame line background to EOL, got %q", output)
	}
}

func TestDisplay_FinalizeWithFrame_FillsLineBgToEOL(t *testing.T) {
	var out bytes.Buffer
	d := NewDisplay(&out, 20, 24)
	d.SetFrame(&InputFrame{
		Prefix:      "P ",
		PrefixWidth: 2,
		LineBg:      "\x1b[48;2;1;2;3m",
	})

	buf := NewBufferFromString("hello")
	cur := NewCursor()
	cur.MoveTo(0, 5)

	d.Render(buf, cur, false)
	out.Reset()
	d.Finalize(buf)

	output := out.String()
	if !strings.Contains(output, "\x1b[48;2;1;2;3m\x1b[K\x1b[0m") {
		t.Fatalf("finalize output should fill frame line background to EOL, got %q", output)
	}
}

func TestDisplay_RenderWithFrame_ClearsFromLineEnd(t *testing.T) {
	var out bytes.Buffer
	d := NewDisplay(&out, 20, 24)
	d.SetFrame(&InputFrame{
		Prefix:      "P ",
		PrefixWidth: 2,
		LineBg:      "\x1b[48;2;1;2;3m",
	})

	buf := NewBufferFromString("hello")
	cur := NewCursor()
	cur.MoveTo(0, 5)

	d.Render(buf, cur, false)
	output := out.String()

	if !strings.Contains(output, "\x1b[48;2;1;2;3m\x1b[J\x1b[0m") {
		t.Fatalf("render should clear-to-end with frame background active, got %q", output)
	}
}

func TestDisplay_RenderWithFrame_UsesBottomLineBgOverride(t *testing.T) {
	var out bytes.Buffer
	d := NewDisplay(&out, 20, 24)
	d.SetFrame(&InputFrame{
		TopLine:      "TOP",
		BottomLine:   "BOT",
		Prefix:       "P ",
		PrefixWidth:  2,
		LineBg:       "\x1b[48;2;1;2;3m",
		BottomLineBg: "\x1b[48;2;7;8;9m",
	})

	buf := NewBufferFromString("hello")
	cur := NewCursor()
	cur.MoveTo(0, 5)

	d.Render(buf, cur, false)
	output := out.String()
	if !strings.Contains(output, "\x1b[48;2;7;8;9m\x1b[K\x1b[0m") {
		t.Fatalf("expected bottom line to use overridden background, got %q", output)
	}
	if !strings.Contains(output, "\x1b[48;2;7;8;9m\x1b[J\x1b[0m") {
		t.Fatalf("expected clear-to-end to use overridden background, got %q", output)
	}
}

func TestDisplay_Render_LongWrappedLineTracksVisualCursorRow(t *testing.T) {
	var out bytes.Buffer
	d := NewDisplay(&out, 10, 24)
	d.SetPrompt("$ ")

	buf := NewBufferFromString("abcdefghijklmno")
	cur := NewCursor()
	cur.MoveTo(0, len("abcdefghijklmno"))

	d.Render(buf, cur, false)
	if d.lastCursorRow != 1 {
		t.Fatalf("lastCursorRow = %d, want 1", d.lastCursorRow)
	}

	out.Reset()
	d.Render(buf, cur, false)
	output := out.String()
	if !strings.Contains(output, "\x1b[1A") {
		t.Fatalf("second render should move up one wrapped row, got %q", output)
	}
}

func TestDisplay_Render_UsesVisualRowsForCursorReposition(t *testing.T) {
	var out bytes.Buffer
	d := NewDisplay(&out, 10, 24)

	buf := NewBufferFromString("abcdefghijklmnopqrst\nz")
	cur := NewCursor()
	cur.MoveTo(0, 0)

	d.Render(buf, cur, false)
	output := out.String()

	if !strings.Contains(output, "\x1b[3A") {
		t.Fatalf("render should move up by wrapped visual rows (3A), got %q", output)
	}
}

func TestDisplay_RenderWithFrame_LongWrappedLineTracksVisualCursorRow(t *testing.T) {
	var out bytes.Buffer
	d := NewDisplay(&out, 10, 24)
	d.SetFrame(&InputFrame{
		TopLine:     "TOP",
		BottomLine:  "BOT",
		Prefix:      "P ",
		PrefixWidth: 2,
	})

	buf := NewBufferFromString("abcdefghijklmno")
	cur := NewCursor()
	cur.MoveTo(0, len("abcdefghijklmno"))

	d.Render(buf, cur, false)
	if d.lastCursorRow != 2 {
		t.Fatalf("lastCursorRow = %d, want 2", d.lastCursorRow)
	}

	out.Reset()
	d.Render(buf, cur, false)
	output := out.String()
	if !strings.Contains(output, "\x1b[2A") {
		t.Fatalf("framed second render should move up two wrapped rows, got %q", output)
	}
}

func TestDisplay_RenderCompletionMenu_WrapsLongColumns(t *testing.T) {
	var out bytes.Buffer
	d := NewDisplay(&out, 10, 24)
	d.SetPrompt("$ ")

	items := []CompletionItem{
		{Text: "file.txt", Description: "A file"},
	}

	d.RenderCompletionMenu(items, 0, 15, 0, 17)
	output := out.String()

	// startCol=15 + prefix=2 => wrapped column 7
	if !strings.Contains(output, "\x1b[7C") {
		t.Fatalf("menu should wrap start column to 7, got %q", output)
	}

	// cursorCol=17 + prefix=2 => wrapped restore column 9
	if !strings.Contains(output, "\x1b[9C") {
		t.Fatalf("menu should restore wrapped cursor column 9, got %q", output)
	}
}
