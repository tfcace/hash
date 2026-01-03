// internal/editor/display.go
package editor

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// ANSI escape sequences
const (
	ansiClearLine     = "\x1b[2K"
	ansiCursorUp      = "\x1b[%dA"
	ansiCursorDown    = "\x1b[%dB"
	ansiCursorForward = "\x1b[%dC"
	ansiCursorBack    = "\x1b[%dD"
	ansiCursorPos     = "\x1b[%d;%dH"
	ansiCursorCol     = "\x1b[%dG"
	ansiReverse       = "\x1b[7m"
	ansiReset         = "\x1b[0m"
	ansiHideCursor    = "\x1b[?25l"
	ansiShowCursor    = "\x1b[?25h"
	ansiClearToEnd    = "\x1b[J"
	ansiBold          = "\x1b[1m"
)

// Display handles terminal rendering.
type Display struct {
	out            io.Writer
	width          int
	height         int
	gutter         bool
	prompt         string // Prompt to display before first line
	promptWidth    int
	lastLines      int    // Number of lines rendered last time
	lastCursorRow  int    // Row where cursor was left (0-indexed from our content start)
	inputBgCode    string // ANSI code for input background color
	finalizedLines int    // Lines rendered by last Finalize call
	scrollbarCode  string // ANSI code for scrollbar foreground color
}

// NewDisplay creates a new display.
func NewDisplay(out io.Writer, width, height int) *Display {
	return &Display{
		out:    out,
		width:  width,
		height: height,
	}
}

// SetGutter enables/disables the gutter indicator.
func (d *Display) SetGutter(enabled bool) {
	d.gutter = enabled
}

// SetPromptWidth sets the prompt width for cursor positioning.
func (d *Display) SetPromptWidth(w int) {
	d.promptWidth = w
}

// SetPrompt sets the prompt string to display before the first line.
func (d *Display) SetPrompt(prompt string) {
	d.prompt = prompt
	// Calculate visible width (strip ANSI codes)
	d.promptWidth = visibleWidth(prompt)
}

// SetInputBgColor sets the background color for submitted input.
// hexColor should be in format "#RRGGBB".
func (d *Display) SetInputBgColor(hexColor string) {
	if hexColor == "" || len(hexColor) != 7 || hexColor[0] != '#' {
		return
	}
	// Parse hex to RGB
	var r, g, b int
	fmt.Sscanf(hexColor[1:], "%02x%02x%02x", &r, &g, &b)
	// Generate ANSI true color background code
	d.inputBgCode = fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
}

// SetScrollbarColor sets the foreground color for scrollbars.
// hexColor should be in format "#RRGGBB".
func (d *Display) SetScrollbarColor(hexColor string) {
	if hexColor == "" || len(hexColor) != 7 || hexColor[0] != '#' {
		return
	}
	// Parse hex to RGB
	var r, g, b int
	fmt.Sscanf(hexColor[1:], "%02x%02x%02x", &r, &g, &b)
	// Generate ANSI true color foreground code
	d.scrollbarCode = fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
}

// visibleWidth returns the visible width of a string, excluding ANSI codes.
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

// Render draws the buffer content with cursor.
func (d *Display) Render(buf *Buffer, cur *Cursor, hasSelection bool) {
	var sb strings.Builder

	// Hide cursor during render for flicker-free updates
	sb.WriteString(ansiHideCursor)

	// Calculate how many lines we need to clear (max of previous and current)
	linesToClear := d.lastLines
	if linesToClear == 0 {
		linesToClear = 1 // First render
	}

	// Move up from where cursor was left to first line of our content
	if d.lastCursorRow > 0 {
		fmt.Fprintf(&sb, ansiCursorUp, d.lastCursorRow)
	}
	sb.WriteString("\r")

	gutterPrefix := " " // Minimal indent for continuation lines
	if d.gutter {
		gutterPrefix = "│ "
	}

	// Render each line (clear as we go)
	maxLines := linesToClear
	if buf.LineCount() > maxLines {
		maxLines = buf.LineCount()
	}

	for i := 0; i < maxLines; i++ {
		if i > 0 {
			sb.WriteString("\r\n")
		}
		sb.WriteString(ansiClearLine)

		// Only render content for lines that exist in buffer
		if i < buf.LineCount() {
			line := buf.Line(i)

			// First line has prompt, others have gutter
			if i == 0 {
				if d.prompt != "" {
					sb.WriteString(d.prompt)
				} else {
					sb.WriteString(gutterPrefix)
				}
			} else {
				sb.WriteString(gutterPrefix)
			}

			// Render line content with optional selection highlighting
			if hasSelection && cur.HasSelection() {
				d.renderLineWithSelection(&sb, line, i, cur)
			} else {
				sb.WriteString(line)
			}
		}
	}

	// If we rendered more lines than buffer has, move back up
	if maxLines > buf.LineCount() {
		fmt.Fprintf(&sb, ansiCursorUp, maxLines-buf.LineCount())
	}

	d.lastLines = buf.LineCount()

	// Position cursor
	cursorRow := cur.Pos.Row
	cursorCol := cur.Pos.Col

	// Move to cursor position
	if cursorRow < buf.LineCount()-1 {
		fmt.Fprintf(&sb, ansiCursorUp, buf.LineCount()-1-cursorRow)
	}

	// Calculate prefix width for cursor positioning
	// First line uses prompt if available, otherwise gutter prefix
	// Other lines always use gutter prefix
	prefixWidth := 1 // Default gutter width (single space)
	if d.gutter {
		prefixWidth = 2 // "│ "
	}
	if cursorRow == 0 && d.promptWidth > 0 {
		prefixWidth = d.promptWidth
	}

	sb.WriteString("\r")
	if cursorCol+prefixWidth > 0 {
		fmt.Fprintf(&sb, ansiCursorForward, cursorCol+prefixWidth)
	}

	// Show cursor
	sb.WriteString(ansiShowCursor)

	// Remember where we left the cursor for next render
	d.lastCursorRow = cursorRow

	d.out.Write([]byte(sb.String()))
}

func (d *Display) renderLineWithSelection(sb *strings.Builder, line string, row int, cur *Cursor) {
	start, end := cur.SelectionRange()

	// Check if this line has any selection
	if row < start.Row || row > end.Row {
		sb.WriteString(line)
		return
	}

	startCol := 0
	endCol := len(line)

	if row == start.Row {
		startCol = start.Col
	}
	if row == end.Row {
		endCol = end.Col
	}

	// Clamp to line bounds
	if startCol > len(line) {
		startCol = len(line)
	}
	if endCol > len(line) {
		endCol = len(line)
	}

	// Render: before | selected | after
	sb.WriteString(line[:startCol])
	sb.WriteString(ansiReverse)
	sb.WriteString(line[startCol:endCol])
	sb.WriteString(ansiReset)
	sb.WriteString(line[endCol:])
}

// Clear clears the display area.
func (d *Display) Clear() {
	if d.lastCursorRow > 0 {
		fmt.Fprintf(d.out, ansiCursorUp, d.lastCursorRow)
	}
	fmt.Fprint(d.out, "\r")
	fmt.Fprint(d.out, ansiClearToEnd)
	d.lastLines = 0
	d.lastCursorRow = 0
}

// Finalize leaves the content on screen and moves to a new line.
// Re-renders with background highlight to distinguish from output.
func (d *Display) Finalize(buf *Buffer) {
	var sb strings.Builder

	// Move cursor to beginning of the displayed content
	if d.lastCursorRow > 0 {
		fmt.Fprintf(&sb, ansiCursorUp, d.lastCursorRow)
	}
	sb.WriteString("\r")

	gutterPrefix := " " // Minimal indent for continuation lines
	gutterWidth := 1
	if d.gutter {
		gutterPrefix = "│ "
		gutterWidth = 2
	}

	// Re-render each line with background highlight
	for i := 0; i < buf.LineCount(); i++ {
		sb.WriteString(ansiClearLine)

		// Apply background color if set
		if d.inputBgCode != "" {
			sb.WriteString(d.inputBgCode)
		}

		var contentWidth int
		if i == 0 {
			if d.prompt != "" {
				sb.WriteString(d.prompt)
				contentWidth = d.promptWidth
			} else {
				sb.WriteString(gutterPrefix)
				contentWidth = gutterWidth
			}
		} else {
			sb.WriteString(gutterPrefix)
			contentWidth = gutterWidth
		}

		// Bold the command text
		sb.WriteString(ansiBold)
		sb.WriteString(buf.Line(i))
		sb.WriteString(ansiReset)
		contentWidth += len(buf.Line(i))

		// Pad to full terminal width for clean block look
		if d.inputBgCode != "" && d.width > contentWidth {
			sb.WriteString(d.inputBgCode)
			padding := d.width - contentWidth
			for j := 0; j < padding; j++ {
				sb.WriteByte(' ')
			}
			sb.WriteString(ansiReset)
		}

		sb.WriteString("\r\n")
	}

	d.out.Write([]byte(sb.String()))
	d.finalizedLines = buf.LineCount()
	d.lastLines = 0
	d.lastCursorRow = 0
}

// FinalizedLines returns how many lines were rendered by Finalize.
// Used to position cursor for post-execution annotations.
func (d *Display) FinalizedLines() int {
	return d.finalizedLines
}

// AnnotateDuration adds execution duration to the right side of the command line.
// Must be called after command execution, with outputLines = lines of command output.
func (d *Display) AnnotateDuration(outputLines int, durationMs int64) {
	if durationMs < 100 { // Don't show for very fast commands
		return
	}

	// Format duration
	var text string
	if durationMs < 1000 {
		text = fmt.Sprintf(" %dms ", durationMs)
	} else if durationMs < 60000 {
		text = fmt.Sprintf(" %.1fs ", float64(durationMs)/1000)
	} else {
		mins := durationMs / 60000
		secs := (durationMs % 60000) / 1000
		text = fmt.Sprintf(" %dm%ds ", mins, secs)
	}

	// Calculate how many lines to go back (output + command lines)
	linesToGoBack := outputLines + d.finalizedLines

	var sb strings.Builder

	// Save cursor, move up, position at right edge
	sb.WriteString("\x1b[s") // Save cursor position
	if linesToGoBack > 0 {
		fmt.Fprintf(&sb, ansiCursorUp, linesToGoBack)
	}

	// Position at right side (column = width - text length)
	col := d.width - len(text)
	if col > 0 {
		fmt.Fprintf(&sb, ansiCursorCol, col)
	}

	// Print duration with dim styling and background
	sb.WriteString("\x1b[2m") // Dim
	if d.inputBgCode != "" {
		sb.WriteString(d.inputBgCode)
	}
	sb.WriteString(text)
	sb.WriteString(ansiReset)

	// Restore cursor position
	sb.WriteString("\x1b[u")

	d.out.Write([]byte(sb.String()))
}

// Resize updates the terminal dimensions.
func (d *Display) Resize(width, height int) {
	d.width = width
	d.height = height
}

// CompletionItem is passed to display for rendering.
type CompletionItem struct {
	Text        string
	Description string
}

// RenderCompletionMenu draws the completion dropdown below the cursor.
func (d *Display) RenderCompletionMenu(items []CompletionItem, selected, startCol int) {
	if len(items) == 0 {
		return
	}

	var sb strings.Builder

	// Save cursor position
	sb.WriteString("\x1b[s")

	// Move down one line for menu
	sb.WriteString("\r\n")

	// Calculate max width for alignment
	maxTextWidth := 0
	for _, item := range items {
		if len(item.Text) > maxTextWidth {
			maxTextWidth = len(item.Text)
		}
	}

	// Limit visible items (show max 6, scrollbar appears for 7+)
	maxVisible := 6
	if len(items) < maxVisible {
		maxVisible = len(items)
	}

	// Calculate scroll offset to keep selected visible
	scrollOffset := 0
	if selected >= maxVisible {
		scrollOffset = selected - maxVisible + 1
	}

	// Calculate scrollbar thumb position and size (only if scrolling needed)
	needsScrollbar := len(items) > maxVisible && d.scrollbarCode != ""
	thumbSize := 1
	thumbStart := 0
	if needsScrollbar {
		// Thumb size proportional to visible/total ratio, minimum 1
		thumbSize = maxVisible * maxVisible / len(items)
		if thumbSize < 1 {
			thumbSize = 1
		}
		// Thumb position based on scroll offset
		scrollRange := len(items) - maxVisible
		thumbRange := maxVisible - thumbSize
		if scrollRange > 0 && thumbRange > 0 {
			thumbStart = scrollOffset * thumbRange / scrollRange
		}
	}

	// Render menu items
	for i := scrollOffset; i < scrollOffset+maxVisible && i < len(items); i++ {
		item := items[i]
		rowIndex := i - scrollOffset // 0-based index within visible area

		// Clear line and position at startCol
		sb.WriteString(ansiClearLine)
		if startCol > 0 {
			fmt.Fprintf(&sb, ansiCursorForward, startCol)
		}

		// Draw scrollbar as first column of menu (before content)
		if needsScrollbar {
			if rowIndex >= thumbStart && rowIndex < thumbStart+thumbSize {
				sb.WriteString(d.scrollbarCode)
				sb.WriteString("▌")
				sb.WriteString(ansiReset)
			} else {
				sb.WriteString(" ")
			}
		}

		// Highlight selected
		if i == selected {
			sb.WriteString(ansiReverse)
		}

		// Draw item
		sb.WriteString(" ")
		sb.WriteString(item.Text)

		// Pad to align descriptions
		padding := maxTextWidth - len(item.Text) + 2
		for j := 0; j < padding; j++ {
			sb.WriteByte(' ')
		}

		// Description (dimmed) - only render if we have room
		if item.Description != "" {
			maxDesc := d.width - startCol - maxTextWidth - 5
			if maxDesc > 3 { // Only render if we have room for at least some text
				if i != selected {
					sb.WriteString("\x1b[2m") // Dim
				}
				// Truncate description if too long (rune-aware for UTF-8 safety)
				desc := item.Description
				if utf8.RuneCountInString(desc) > maxDesc {
					runes := []rune(desc)
					desc = string(runes[:maxDesc-3]) + "..."
				}
				sb.WriteString(desc)
				if i != selected {
					sb.WriteString(ansiReset)
				}
			}
		}

		sb.WriteString(" ")

		if i == selected {
			sb.WriteString(ansiReset)
		}

		// Move to next line (except last)
		if i < scrollOffset+maxVisible-1 && i < len(items)-1 {
			sb.WriteString("\r\n")
		}
	}

	// Restore cursor position
	sb.WriteString("\x1b[u")

	d.out.Write([]byte(sb.String()))
}

// ClearCompletionMenu removes the completion menu from display.
func (d *Display) ClearCompletionMenu(numItems int) {
	if numItems == 0 {
		return
	}

	var sb strings.Builder

	// Save cursor
	sb.WriteString("\x1b[s")

	// Move down and clear each menu line
	maxVisible := 6
	if numItems < maxVisible {
		maxVisible = numItems
	}

	for i := 0; i < maxVisible; i++ {
		sb.WriteString("\r\n")
		sb.WriteString(ansiClearLine)
	}

	// Restore cursor
	sb.WriteString("\x1b[u")

	d.out.Write([]byte(sb.String()))
}
