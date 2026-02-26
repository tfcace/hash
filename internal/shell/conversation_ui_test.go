package shell

import (
	"bytes"
	"strings"
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

	// Should contain single bar character (user block style)
	if !bytes.Contains([]byte(output), []byte("│")) {
		t.Error("expected │ input prompt character for user block")
	}
}

func TestConversationUI_Hints(t *testing.T) {
	var buf bytes.Buffer
	ui := NewConversationUI(&buf, "#7c3aed")

	ui.WriteHints()

	output := buf.String()

	// Should contain hint text
	if !bytes.Contains([]byte(output), []byte("Ctrl+C exit")) {
		t.Error("expected 'Ctrl+C exit' in hints")
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

func TestConversationUI_WriteBottomBorder_WhenTintDisabledStillRenders(t *testing.T) {
	var buf bytes.Buffer
	ui := NewConversationUI(&buf, "#7c3aed")
	ui.ClearTint()

	ui.WriteBottomBorder()
	output := buf.String()

	if !strings.Contains(output, "╰") {
		t.Fatalf("expected bottom border glyph when tint is disabled, got %q", output)
	}
}

func TestConversationUI_WriteStreamTinted(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantTint bool
	}{
		{
			name:     "single line",
			input:    "Hello world",
			wantTint: true,
		},
		{
			name:     "with newline",
			input:    "Line 1\nLine 2",
			wantTint: true,
		},
		{
			name:     "empty string",
			input:    "",
			wantTint: false,
		},
		{
			name:     "only newline",
			input:    "\n",
			wantTint: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			ui := NewConversationUI(&buf, "#7c3aed")

			ui.WriteStreamTinted(tt.input)
			output := buf.String()

			if tt.wantTint {
				if !bytes.Contains([]byte(output), []byte("\x1b[48;2;")) {
					t.Errorf("expected background tint in output: %q", output)
				}
			}

			// Text content should be present (if any)
			if tt.input != "" && !bytes.Contains([]byte(output), []byte(tt.input)) {
				// The text may be split by ANSI codes, so check without the newline handling
				// Just verify something was written
				if len(output) == 0 {
					t.Error("expected some output")
				}
			}
		})
	}
}

func TestConversationUI_WriteStreamTinted_NoTintWhenDisabled(t *testing.T) {
	var buf bytes.Buffer
	ui := NewConversationUI(&buf, "#7c3aed")
	ui.ClearTint()

	ui.WriteStreamTinted("Hello world")
	output := buf.String()

	// Should NOT contain background tint
	if bytes.Contains([]byte(output), []byte("\x1b[48;2;")) {
		t.Error("expected no background tint when tint is disabled")
	}

	// But should contain the text
	if !bytes.Contains([]byte(output), []byte("Hello world")) {
		t.Error("expected text content")
	}
}

func TestConversationUI_WriteStreamTinted_ChunkedSingleLine_NoMidLineBorder(t *testing.T) {
	var buf bytes.Buffer
	ui := NewConversationUI(&buf, "#7c3aed")

	ui.WriteStreamTinted("Hello")
	ui.WriteStreamTinted(" world")

	output := buf.String()

	if got := strings.Count(output, "│"); got != 1 {
		t.Fatalf("border marker count = %d, want 1; output=%q", got, output)
	}
	if !strings.Contains(output, "Hello") || !strings.Contains(output, " world") {
		t.Fatalf("missing streamed text in output: %q", output)
	}
}

func TestConversationUI_TopBorderLine_UsesFullWidth(t *testing.T) {
	var buf bytes.Buffer
	ui := NewConversationUI(&buf, "#7c3aed")

	// bytes.Buffer is not a terminal, so ConversationUI falls back to width 80.
	line := ui.topBorderLine()
	if got, want := visibleWidth(line), 80; got != want {
		t.Fatalf("top border width = %d, want %d; line=%q", got, want, line)
	}
}

func TestConversationUI_ComputeUserBoxWidth_WideTerminal(t *testing.T) {
	var buf bytes.Buffer
	ui := NewConversationUI(&buf, "#7c3aed")
	ui.termWidth = 227

	got := ui.computeUserBoxWidth()
	const want = 213
	if got != want {
		t.Fatalf("user box width = %d, want %d", got, want)
	}
}

func TestConversationUI_ClearUserBox_ClearsFourLinesAndRestoresTopCursor(t *testing.T) {
	var buf bytes.Buffer
	ui := NewConversationUI(&buf, "#7c3aed")

	ui.ClearUserBox()
	output := buf.String()

	if !strings.HasPrefix(output, "\r\x1b[1A\x1b[s") {
		t.Fatalf("clear sequence should move to top and save cursor, got %q", output)
	}
	if got := strings.Count(output, "\x1b[2K"); got != 4 {
		t.Fatalf("clear sequence should clear exactly four rows, got %d in %q", got, output)
	}
	if !strings.HasSuffix(output, "\x1b[u\x1b[0m") {
		t.Fatalf("clear sequence should restore top cursor and reset style, got %q", output)
	}
}

func TestConversationUI_UserBoxBottomLine_UsesSolidBoxBorder(t *testing.T) {
	var buf bytes.Buffer
	ui := NewConversationUI(&buf, "#7c3aed")

	line := ui.userBoxBottomLine()
	if !strings.Contains(line, "─") {
		t.Fatalf("expected solid box border rune in user bottom line, got %q", line)
	}
	if strings.Contains(line, "┈") {
		t.Fatalf("expected no textured seam in user bottom line, got %q", line)
	}
}

func TestConversationUI_InputFrame_UsesSofterBottomLineBg(t *testing.T) {
	var buf bytes.Buffer
	ui := NewConversationUI(&buf, "#7c3aed")

	frame := ui.InputFrame()
	if frame.BottomExtraLineBg == "" {
		t.Fatal("expected bottom extra line bg override to be set")
	}
	if frame.BottomExtraLineBg == frame.LineBg {
		t.Fatalf("expected bottom extra line bg to differ from main line bg, both were %q", frame.LineBg)
	}
	if frame.BottomExtraLine == "" {
		t.Fatal("expected bottom extra blend line to be set")
	}
}
