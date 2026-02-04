package shell

import (
	"fmt"
	"io"
)

// ConversationUI renders the tinted conversation zone.
type ConversationUI struct {
	out         io.Writer
	accentColor string
	tintBg      string // Pre-computed background escape sequence
	tintActive  bool
}

// NewConversationUI creates a conversation UI with the given accent color.
// accentColor should be a hex color like "#7c3aed".
func NewConversationUI(out io.Writer, accentColor string) *ConversationUI {
	ui := &ConversationUI{
		out:         out,
		accentColor: accentColor,
		tintActive:  true,
	}
	ui.tintBg = ui.computeTintBackground()
	return ui
}

// computeTintBackground derives a subtle background tint from the accent color.
func (ui *ConversationUI) computeTintBackground() string {
	// Parse hex color
	var r, g, b int
	if len(ui.accentColor) == 7 && ui.accentColor[0] == '#' {
		fmt.Sscanf(ui.accentColor[1:], "%02x%02x%02x", &r, &g, &b)
	} else {
		// Fallback to a subtle dark blue-gray
		r, g, b = 30, 30, 46 // #1e1e2e
	}

	// Blend with dark background at ~15% opacity
	// Assuming terminal background is ~#1a1a1a (26, 26, 26)
	bgR, bgG, bgB := 26, 26, 26
	blend := 0.15
	finalR := int(float64(bgR)*(1-blend) + float64(r)*blend)
	finalG := int(float64(bgG)*(1-blend) + float64(g)*blend)
	finalB := int(float64(bgB)*(1-blend) + float64(b)*blend)

	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", finalR, finalG, finalB)
}

// WriteTintedLine writes a line with background tint.
func (ui *ConversationUI) WriteTintedLine(text string) {
	if ui.tintActive {
		fmt.Fprintf(ui.out, "%s%s\x1b[0m\n", ui.tintBg, text)
	} else {
		fmt.Fprintln(ui.out, text)
	}
}

// WriteInputPrompt writes the ║ input prompt in accent color.
func (ui *ConversationUI) WriteInputPrompt() {
	// Parse accent color for foreground
	var r, g, b int
	if len(ui.accentColor) == 7 && ui.accentColor[0] == '#' {
		fmt.Sscanf(ui.accentColor[1:], "%02x%02x%02x", &r, &g, &b)
	} else {
		r, g, b = 124, 58, 237 // Default purple
	}

	fgColor := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)

	if ui.tintActive {
		fmt.Fprintf(ui.out, "%s%s║\x1b[0m%s ", ui.tintBg, fgColor, ui.tintBg)
	} else {
		fmt.Fprintf(ui.out, "%s║\x1b[0m ", fgColor)
	}
}

// WriteHints writes the contextual hint footer.
func (ui *ConversationUI) WriteHints() {
	hints := "\x1b[90mEsc exit · !cmd shell\x1b[0m"
	if ui.tintActive {
		// Right-align hints (assuming ~60 char width)
		fmt.Fprintf(ui.out, "%s%40s%s\n", ui.tintBg, "", hints)
	} else {
		fmt.Fprintf(ui.out, "%40s%s\n", "", hints)
	}
}

// ClearTint disables the background tint for future writes.
// Existing content on screen is not affected.
func (ui *ConversationUI) ClearTint() {
	ui.tintActive = false
}

// SetTintActive enables or disables the background tint.
func (ui *ConversationUI) SetTintActive(active bool) {
	ui.tintActive = active
}
