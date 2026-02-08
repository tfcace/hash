package shell

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/tfcace/hash/internal/editor"
	"github.com/tfcace/hash/internal/trace"
	"golang.org/x/term"
)

// ConversationUI renders the tinted conversation zone.
// Methods that write to out are protected by mu to prevent interleaving
// between the spinner goroutine and the main goroutine.
type ConversationUI struct {
	mu           sync.Mutex
	out          io.Writer
	accentColor  string
	tintBg       string // Pre-computed background escape sequence
	border       string // Pre-computed border prefix (│ in accent color + space)
	borderFg     string // Accent foreground color
	userBorder   string // Dimmer border for user box
	resetTint    string // Reset + re-apply tint background
	tintActive   bool
	userIndent   string
	termWidth    int
	userBoxWidth int
}

// NewConversationUI creates a conversation UI with the given accent color.
// accentColor should be a hex color like "#7c3aed".
func NewConversationUI(out io.Writer, accentColor string) *ConversationUI {
	ui := &ConversationUI{
		out:         out,
		accentColor: accentColor,
		tintActive:  true,
		userIndent:  "  ",
	}
	ui.tintBg = ComputeTintBackground(accentColor)
	ui.resetTint = "\x1b[0m" + ui.tintBg

	// Compute colored border: │ in accent color (single line)
	var r, g, b int
	if len(accentColor) == 7 && accentColor[0] == '#' {
		if _, err := fmt.Sscanf(accentColor[1:], "%02x%02x%02x", &r, &g, &b); err != nil {
			r, g, b = 124, 58, 237 // Fallback on parse error
		}
	} else {
		r, g, b = 124, 58, 237
	}
	ui.borderFg = fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
	ui.border = fmt.Sprintf("%s│\x1b[0m%s ", ui.borderFg, ui.tintBg)

	// Dimmer border for user box (50% brightness)
	dimR, dimG, dimB := r/2, g/2, b/2
	ui.userBorder = fmt.Sprintf("\x1b[38;2;%d;%d;%dm", dimR, dimG, dimB)

	ui.refreshTermWidth()
	return ui
}

// WriteTintedLine writes a line with background tint and border.
func (ui *ConversationUI) WriteTintedLine(text string) {
	if ui.tintActive {
		fmt.Fprintf(ui.out, "%s%s%s\x1b[K\x1b[0m\n", ui.tintBg, ui.border, text)
	} else {
		fmt.Fprintln(ui.out, text)
	}
}

// WriteTopBorder writes a decorative top border for the conversation zone.
// Includes hints integrated into the border.
func (ui *ConversationUI) WriteTopBorder() {
	if !ui.tintActive {
		return
	}
	line := ui.topBorderLine()
	fmt.Fprintf(ui.out, "%s%s%s\x1b[K\x1b[0m\n", ui.tintBg, ui.borderFg, line)
}

// WriteBottomBorder writes a decorative bottom border for the conversation zone.
func (ui *ConversationUI) WriteBottomBorder() {
	if !ui.tintActive {
		return
	}
	line := ui.bottomBorderLine()
	fmt.Fprintf(ui.out, "%s%s%s\x1b[K\x1b[0m\n", ui.tintBg, ui.borderFg, line)
}

// InputPromptString returns the prompt string for readline (with ANSI codes).
// This is for the user input line inside their distinct block.
// Uses \001 and \002 markers to tell readline which bytes are non-printing.
func (ui *ConversationUI) InputPromptString() string {
	// \001 and \002 mark non-printing sequences for readline width calculation
	// Indent + border │ + space, with tint background
	wrap := func(s string) string {
		return "\001" + s + "\002"
	}
	return wrap(ui.tintBg) +
		wrap(ui.borderFg) + "│" +
		wrap(ui.resetTint) + " " +
		ui.userIndent +
		wrap(ui.userBorder) + "│" +
		wrap(ui.resetTint) + " "
}

// InputFrame returns the editor input frame for conversation replies.
func (ui *ConversationUI) InputFrame() *editor.InputFrame {
	ui.refreshTermWidth()
	prefix, prefixWidth := ui.userBoxPrefix()

	frame := &editor.InputFrame{
		TopLine:     ui.userBoxTopLine(),
		BottomLine:  ui.userBoxBottomLine(),
		Prefix:      prefix,
		PrefixWidth: prefixWidth,
		LineBg:      ui.tintBg,
	}
	if trace.Enabled("shell") {
		trace.ShellHigh("conversation_input_frame", map[string]any{
			"term_width":           ui.termWidth,
			"user_box_width":       ui.userBoxWidth,
			"prefix_width":         prefixWidth,
			"top_visible_width":    visibleWidth(frame.TopLine),
			"bottom_visible_width": visibleWidth(frame.BottomLine),
			"line_bg":              frame.LineBg != "",
			"tint_active":          ui.tintActive,
		})
	}
	return frame
}

// WriteUserBoxTop draws the top line of the user input box.
func (ui *ConversationUI) WriteUserBoxTop() {
	if !ui.tintActive {
		return
	}

	fmt.Fprintf(ui.out, "%s\n", ui.userBoxTopLine())
}

// WriteUserBoxBottom draws the bottom line of the user input box.
func (ui *ConversationUI) WriteUserBoxBottom() {
	if !ui.tintActive {
		return
	}
	if ui.userBoxWidth == 0 {
		ui.refreshTermWidth()
	}

	fmt.Fprintf(ui.out, "%s\n", ui.userBoxBottomLine())
}

// FinishUserBox closes the box and adds breathing room after input is complete.
func (ui *ConversationUI) FinishUserBox() {
	if !ui.tintActive {
		fmt.Fprintln(ui.out)
		return
	}
	ui.WriteUserBoxBottom()
	// Empty tinted line for breathing room
	fmt.Fprintf(ui.out, "%s\x1b[K\x1b[0m\n", ui.tintBg)
}

// ClearUserBox erases the user input box (top + input line) when canceling.
func (ui *ConversationUI) ClearUserBox() {
	// Move to top of box (up 2 from content line) and clear 3 lines
	fmt.Fprint(ui.out, "\x1b[2K")        // Clear current line
	fmt.Fprint(ui.out, "\x1b[1A\x1b[2K") // Up, clear (bottom border)
	fmt.Fprint(ui.out, "\x1b[1A\x1b[2K") // Up, clear (top border)
	fmt.Fprint(ui.out, "\x1b[0m")        // Reset
}

// WriteInputPrompt writes the ║ input prompt in accent color.
func (ui *ConversationUI) WriteInputPrompt() {
	fmt.Fprint(ui.out, ui.InputPromptString())
}

// WriteHints writes the contextual hint footer with breathing room.
func (ui *ConversationUI) WriteHints() {
	hints := "\x1b[90mCtrl+C exit · !cmd shell · /done finish\x1b[0m"
	if ui.tintActive {
		// Empty line for breathing room
		fmt.Fprintf(ui.out, "%s\x1b[K\x1b[0m\n", ui.tintBg)
		// Right-aligned hints (no border, just tint)
		fmt.Fprintf(ui.out, "%s%40s%s\x1b[K\x1b[0m\n", ui.tintBg, "", hints)
	} else {
		fmt.Println()
		fmt.Fprintf(ui.out, "%30s%s\n", "", hints)
	}
}

// WriteCancelHint shows the "press again to exit" hint after Ctrl+C.
func (ui *ConversationUI) WriteCancelHint() {
	if ui.tintActive {
		// Keep background tint active; only set foreground color.
		ui.WriteTintedLine("\x1b[90mPress Ctrl+C again to exit\x1b[39m")
		return
	}
	fmt.Fprintln(ui.out, "Press Ctrl+C again to exit")
}

// WriteIdleTimeout shows a message when conversation mode exits due to idle timeout.
func (ui *ConversationUI) WriteIdleTimeout() {
	if ui.tintActive {
		ui.WriteTintedLine("\x1b[90mConversation ended (idle timeout)\x1b[39m")
		return
	}
	fmt.Fprintln(ui.out, "Conversation ended (idle timeout)")
}

// WriteThinkingIndicator displays a thinking/spinner message with tinting.
// Called from the spinner goroutine — acquires mu to avoid interleaving
// with ClearThinkingIndicator or WriteStreamTinted on the main goroutine.
func (ui *ConversationUI) WriteThinkingIndicator(char rune, text string) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	if ui.tintActive {
		fmt.Fprintf(ui.out, "\r%s%s\x1b[90m%c %s\x1b[0m%s\x1b[K", ui.tintBg, ui.border, char, text, ui.tintBg)
	} else {
		fmt.Fprintf(ui.out, "\r\x1b[90m%c %s\x1b[0m\x1b[K", char, text)
	}
}

// ClearThinkingIndicator clears the thinking indicator line.
// Called from the main goroutine after canceling the spinner context and
// waiting for it to exit, but mu ensures no in-flight tick interleaves.
func (ui *ConversationUI) ClearThinkingIndicator() {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	if ui.tintActive {
		fmt.Fprintf(ui.out, "\r%s%s\x1b[K\x1b[0m", ui.tintBg, ui.border)
	} else {
		fmt.Fprint(ui.out, "\r\x1b[K")
	}
}

// ClearTint disables the background tint for future writes.
// Existing content on screen is not affected.
func (ui *ConversationUI) ClearTint() {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.tintActive = false
}

// SetTintActive enables or disables the background tint.
func (ui *ConversationUI) SetTintActive(active bool) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.tintActive = active
}

// WriteStreamTinted writes streamed text with background tint and border.
// Handles partial chunks that may or may not contain newlines.
func (ui *ConversationUI) WriteStreamTinted(text string) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	if !ui.tintActive || text == "" {
		fmt.Fprint(ui.out, text)
		return
	}

	// Start with tint and border
	fmt.Fprint(ui.out, ui.tintBg+ui.border)

	// The markdown renderer adds \x1b[0m resets which wipe our background.
	// We need to reapply tint after every reset.
	tinted := strings.ReplaceAll(text, "\x1b[0m", "\x1b[0m"+ui.tintBg)

	// For newlines: fill to end of line, then newline, then reapply tint and border
	tinted = strings.ReplaceAll(tinted, "\n", "\x1b[K\n"+ui.tintBg+ui.border)

	fmt.Fprint(ui.out, tinted)
}

// TintBg returns the computed background tint escape sequence.
func (ui *ConversationUI) TintBg() string {
	if ui.tintActive {
		return ui.tintBg
	}
	return ""
}

// StreamBorder returns the conversation border prefix for streaming.
func (ui *ConversationUI) StreamBorder() string {
	if ui.tintActive {
		return ui.border
	}
	return ""
}

func (ui *ConversationUI) refreshTermWidth() {
	width := terminalWidth(ui.out)
	if width == 0 {
		width = 80
	}
	if width != ui.termWidth {
		ui.termWidth = width
		ui.userBoxWidth = ui.computeUserBoxWidth()
	}
	if ui.userBoxWidth == 0 {
		ui.userBoxWidth = ui.computeUserBoxWidth()
	}
}

func (ui *ConversationUI) computeUserBoxWidth() int {
	const minWidth = 46
	labelWidth := len("you")
	prefixWidth := 2 + len(ui.userIndent)
	topFixed := prefixWidth + 2 + labelWidth + 1 // "┌ " + label + " "
	available := ui.termWidth - topFixed
	if available < 0 {
		available = 0
	}

	// Keep a small right margin so the box feels inset but still spans most
	// of the conversation width on large terminals.
	margin := 4
	if ui.termWidth <= 80 {
		margin = 6
	}
	target := available - margin
	if target < minWidth {
		if available < minWidth {
			return available
		}
		return minWidth
	}
	if target > available {
		return available
	}
	return target
}

func (ui *ConversationUI) topBorderLine() string {
	ui.refreshTermWidth()
	width := ui.termWidth
	if width <= 0 {
		width = 80
	}

	left := "╭─── conversation "
	right := " ───"
	hint := "Ctrl+C exit · !cmd shell · /done finish"

	leftWidth := visibleWidth(left)
	rightWidth := visibleWidth(right)
	hintWidth := visibleWidth(hint)

	minLen := leftWidth + rightWidth
	if width < minLen {
		if width <= leftWidth {
			return truncateWidth(left, width)
		}
		return left + strings.Repeat("─", width-leftWidth)
	}

	fixedWithHint := leftWidth + 1 + hintWidth + rightWidth
	if width >= fixedWithHint {
		filler := width - fixedWithHint
		if filler < 0 {
			filler = 0
		}
		return left +
			strings.Repeat("─", filler) +
			" " +
			"\x1b[90m" + hint + ui.resetTint + ui.borderFg +
			right
	}

	filler := width - minLen
	if filler < 0 {
		filler = 0
	}
	return left + strings.Repeat("─", filler) + right
}

// terminalWidth returns the best-known terminal width for the current UI stream.
// It prefers the configured output file descriptor, then falls back to stdio FDs.
func terminalWidth(out io.Writer) int {
	candidates := make([]*os.File, 0, 4)
	if f, ok := out.(*os.File); ok {
		candidates = append(candidates, f)
	}
	candidates = append(candidates, os.Stdout, os.Stdin, os.Stderr)

	seen := map[uintptr]struct{}{}
	for _, f := range candidates {
		if f == nil {
			continue
		}
		fd := f.Fd()
		if _, ok := seen[fd]; ok {
			continue
		}
		seen[fd] = struct{}{}
		if !term.IsTerminal(int(fd)) {
			continue
		}
		if w, _, err := term.GetSize(int(fd)); err == nil && w > 0 {
			return w
		}
	}

	return 0
}

// visibleWidth returns the visible character width of s, excluding ANSI escapes.
func visibleWidth(s string) int {
	width := 0
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		width++
	}
	return width
}

func truncateWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	return string(runes[:width])
}

func (ui *ConversationUI) bottomBorderLine() string {
	ui.refreshTermWidth()
	width := ui.termWidth
	if width <= 0 {
		width = 80
	}
	if width <= 1 {
		return "╰"
	}
	return "╰" + strings.Repeat("─", width-1)
}

func (ui *ConversationUI) userBoxPrefix() (prefix string, prefixWidth int) {
	prefix = ui.tintBg + ui.border + ui.userIndent + ui.userBorder + "│" + ui.resetTint + " "
	prefixWidth = 2 + len(ui.userIndent) + 2
	return prefix, prefixWidth
}

func (ui *ConversationUI) userBoxTopLine() string {
	ui.refreshTermWidth()
	label := "you"
	labelWidth := utf8.RuneCountInString(label)
	lineWidth := ui.userBoxWidth - (labelWidth + 2) // space on both sides of label
	if lineWidth < 0 {
		lineWidth = 0
	}
	line := strings.Repeat("─", lineWidth)
	prefix := ui.tintBg + ui.border + ui.userIndent
	return fmt.Sprintf("%s%s┌%s \x1b[90m%s%s %s%s%s\x1b[K\x1b[0m",
		prefix,
		ui.userBorder,
		ui.resetTint,
		label,
		ui.resetTint,
		ui.userBorder,
		line,
		ui.resetTint,
	)
}

func (ui *ConversationUI) userBoxBottomLine() string {
	ui.refreshTermWidth()
	line := strings.Repeat("─", ui.userBoxWidth)
	prefix := ui.tintBg + ui.border + ui.userIndent
	return fmt.Sprintf("%s%s└%s%s\x1b[K\x1b[0m",
		prefix,
		ui.userBorder,
		line,
		ui.resetTint,
	)
}
