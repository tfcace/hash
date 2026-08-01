// internal/editor/display.go
package editor

import (
	"fmt"
	"io"
	"strings"

	"github.com/mattn/go-runewidth"
	"github.com/tfcace/hash/internal/trace"
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
	mode           string // Current editor mode ("insert", "normal")
	prompt         string // Prompt to display before first line
	promptWidth    int
	lastLines      int    // Number of lines rendered last time
	lastCursorRow  int    // Row where cursor was left (0-indexed from our content start)
	inputBgCode    string // ANSI code for input background color
	finalizedLines int    // Lines rendered by last Finalize call
	scrollbarCode  string // ANSI code for scrollbar foreground color
	lastMenuItems  int    // Number of items in last rendered completion menu
	frame          *InputFrame
}

// InputFrame customizes how the input area is rendered.
// PrefixWidth is the visible width of Prefix (ANSI excluded).
type InputFrame struct {
	TopLine           string // Rendered above input lines (no trailing newline)
	LiveTopLine       string // Optional line rendered above input only while editing
	BottomLine        string // Rendered below input lines (no trailing newline)
	BottomExtraLine   string // Optional line rendered below BottomLine
	Prefix            string // Rendered before each input line
	LivePrefix        string // Optional Prefix override used only while editing
	PrefixWidth       int
	LineBg            string // Optional ANSI background code for line padding
	BottomLineBg      string // Optional ANSI background override for BottomLine
	BottomExtraLineBg string // Optional ANSI background override for BottomExtraLine
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

// SetMode sets the current editor mode for gutter display.
func (d *Display) SetMode(mode string) {
	d.mode = mode
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

// SetFrame configures a custom frame for input rendering.
func (d *Display) SetFrame(frame *InputFrame) {
	d.frame = frame
}

// SetInputBgColor sets the background color for submitted input.
// hexColor should be in format "#RRGGBB".
func (d *Display) SetInputBgColor(hexColor string) {
	if hexColor == "" || len(hexColor) != 7 || hexColor[0] != '#' {
		return
	}
	// Parse hex to RGB
	var r, g, b int
	fmt.Sscanf(hexColor[1:], "%02x%02x%02x", &r, &g, &b) //nolint:errcheck // hex format already validated
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
	fmt.Sscanf(hexColor[1:], "%02x%02x%02x", &r, &g, &b) //nolint:errcheck // hex format already validated
	// Generate ANSI true color foreground code
	d.scrollbarCode = fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
}

// calcPrefixWidth calculates the prefix width for cursor positioning on a given row.
// Structure: [mode bar "i│" or "n│" if gutter] + [prompt on line 0, space on others]
func (d *Display) calcPrefixWidth(row int) int {
	if d.frame != nil {
		return d.frame.PrefixWidth
	}
	prefixWidth := 0
	if d.gutter {
		prefixWidth = 2 // Mode bar "i│" or "n│"
	}
	if row == 0 && d.promptWidth > 0 {
		prefixWidth += d.promptWidth
	} else {
		prefixWidth += 1 // Space after bar (for no prompt on row 0 or continuation lines)
	}
	return prefixWidth
}

// visibleWidth returns the visible width of a string, excluding ANSI codes.
func visibleWidth(s string) int {
	width := 0
	inEscape := false
	var text strings.Builder

	flushText := func() {
		if text.Len() == 0 {
			return
		}
		width += runewidth.StringWidth(text.String())
		text.Reset()
	}

	for _, r := range s {
		if r == '\x1b' {
			flushText()
			inEscape = true
			continue
		}
		if inEscape {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		text.WriteRune(r)
	}
	flushText()
	return width
}

// wrapWidth returns a safe wrapping width for terminal calculations.
func (d *Display) wrapWidth() int {
	if d.width <= 0 {
		return 1
	}
	return d.width
}

// visualRowsForChars returns how many terminal rows are consumed by chars columns.
func (d *Display) visualRowsForChars(chars int) int {
	if chars <= 0 {
		return 1
	}
	return (chars-1)/d.wrapWidth() + 1
}

// wrappedCursorForChars maps a logical character offset to wrapped row/column.
// Returned column is a CSI nC distance from start-of-line after \r.
func (d *Display) wrappedCursorForChars(chars int) (rowOffset, col int) {
	if chars <= 0 {
		return 0, 0
	}

	width := d.wrapWidth()
	rowOffset = chars / width
	col = chars % width
	if col == 0 {
		rowOffset--
		col = width
	}
	return rowOffset, col
}

// clampCursorToBuffer keeps cursor coordinates in bounds for layout math.
func clampCursorToBuffer(buf *Buffer, cur *Cursor) (row, col int) {
	lineCount := buf.LineCount()
	if lineCount <= 0 {
		return 0, 0
	}

	row = cur.Pos.Row
	if row < 0 {
		row = 0
	} else if row >= lineCount {
		row = lineCount - 1
	}

	lineLen := len(buf.Line(row))
	col = cur.Pos.Col
	if col < 0 {
		col = 0
	} else if col > lineLen {
		col = lineLen
	}
	col = clampByteIndexToRuneBoundary(buf.Line(row), col)

	return row, col
}

func visibleWidthAtByteIndex(s string, col int) int {
	col = clampByteIndexToRuneBoundary(s, col)
	return visibleWidth(s[:col])
}

// renderedGhostSuffixWidth returns the visible width added by ghost rendering.
func renderedGhostSuffixWidth(ghostText string, streaming, fromAgent bool) int {
	if ghostText == "" {
		if streaming {
			return visibleWidth(" Agent thinking...")
		}
		return 0
	}

	ghostFirstLine := ghostText
	if newlineIdx := strings.Index(ghostText, "\n"); newlineIdx >= 0 {
		ghostFirstLine = ghostText[:newlineIdx]
	}

	width := visibleWidth(ghostFirstLine)
	if fromAgent {
		if streaming {
			width += visibleWidth("▌")
		} else {
			width += visibleWidth("   [enter]run  [tab]edit  [esc]")
		}
	}

	return width
}

// layoutForStandardRender computes visual cursor/line positions for wrapped input.
func (d *Display) layoutForStandardRender(buf *Buffer, cur *Cursor, cursorLineExtraWidth int) (totalRows, cursorVisualRow, cursorVisualCol int) {
	lineCount := buf.LineCount()
	if lineCount <= 0 {
		return 1, 0, 0
	}

	cursorRow, cursorCol := clampCursorToBuffer(buf, cur)
	totalRows = 0
	cursorVisualRow = 0
	cursorVisualCol = 0

	for i := 0; i < lineCount; i++ {
		prefixWidth := d.calcPrefixWidth(i)
		line := buf.Line(i)
		lineWidth := prefixWidth + visibleWidth(line)

		if i == cursorRow {
			cursorChars := prefixWidth + visibleWidthAtByteIndex(line, cursorCol)
			rowOffset, col := d.wrappedCursorForChars(cursorChars)
			cursorVisualRow = totalRows + rowOffset
			cursorVisualCol = col
			lineWidth += cursorLineExtraWidth
		}

		totalRows += d.visualRowsForChars(lineWidth)
	}

	return totalRows, cursorVisualRow, cursorVisualCol
}

// layoutForFrameRender computes visual cursor/line positions for wrapped framed input.
func (d *Display) layoutForFrameRender(buf *Buffer, cur *Cursor, frame *InputFrame) (totalRows, cursorVisualRow, cursorVisualCol int) {
	lineCount := buf.LineCount()
	if lineCount <= 0 {
		return 1, 0, 0
	}

	cursorRow, cursorCol := clampCursorToBuffer(buf, cur)
	totalRows = 0

	if frame.TopLine != "" {
		totalRows += d.visualRowsForChars(visibleWidth(frame.TopLine))
	}
	if frame.LiveTopLine != "" {
		totalRows += d.visualRowsForChars(visibleWidth(frame.LiveTopLine))
	}

	cursorVisualRow = totalRows
	cursorVisualCol = 0

	for i := 0; i < lineCount; i++ {
		line := buf.Line(i)
		lineWidth := frame.PrefixWidth + visibleWidth(line)

		if i == cursorRow {
			cursorChars := frame.PrefixWidth + visibleWidthAtByteIndex(line, cursorCol)
			rowOffset, col := d.wrappedCursorForChars(cursorChars)
			cursorVisualRow = totalRows + rowOffset
			cursorVisualCol = col
		}

		totalRows += d.visualRowsForChars(lineWidth)
	}

	if frame.BottomLine != "" {
		totalRows += d.visualRowsForChars(visibleWidth(frame.BottomLine))
	}
	if frame.BottomExtraLine != "" {
		totalRows += d.visualRowsForChars(visibleWidth(frame.BottomExtraLine))
	}

	return totalRows, cursorVisualRow, cursorVisualCol
}

// Render draws the buffer content with cursor.
func (d *Display) Render(buf *Buffer, cur *Cursor, hasSelection bool) {
	if d.frame != nil {
		d.renderWithFrame(buf, cur, hasSelection, "", false, false, "")
		return
	}
	var sb strings.Builder

	// Hide cursor during render for flicker-free updates
	sb.WriteString(ansiHideCursor)

	// Move up from where cursor was left to first line of our content
	if d.lastCursorRow > 0 {
		fmt.Fprintf(&sb, ansiCursorUp, d.lastCursorRow)
	}
	sb.WriteString("\r")

	// Mode indicator: letter + vertical bar with color
	// Insert mode: dim "i│" (unobtrusive, normal typing)
	// Normal mode: bold yellow "n│" (attention, command mode)
	modeBar := ""
	if d.gutter {
		switch d.mode {
		case "normal":
			modeBar = "\x1b[33;1mn│\x1b[0m" // Bold yellow "n│"
		default: // "insert" or empty
			modeBar = "\x1b[2mi│\x1b[0m" // Dim "i│"
		}
	}

	for i := 0; i < buf.LineCount(); i++ {
		if i > 0 {
			sb.WriteString("\r\n")
		}
		sb.WriteString(ansiClearLine)

		line := buf.Line(i)

		// Show mode bar on all lines, then prompt on first line
		if d.gutter {
			sb.WriteString(modeBar)
		}
		if i == 0 {
			if d.prompt != "" {
				sb.WriteString(d.prompt)
			} else {
				sb.WriteString(" ") // Space after bar if no prompt
			}
		} else {
			sb.WriteString(" ") // Space after bar for continuation
		}

		// Render line content with optional selection highlighting
		if hasSelection && cur.HasSelection() {
			d.renderLineWithSelection(&sb, line, i, cur)
		} else {
			sb.WriteString(line)
		}
	}

	// Clear everything below the buffer (including any old menu)
	sb.WriteString(ansiClearToEnd)

	// Position cursor using wrapped visual layout rather than logical rows.
	totalRows, cursorVisualRow, cursorVisualCol := d.layoutForStandardRender(buf, cur, 0)
	linesBelowCursor := totalRows - 1 - cursorVisualRow
	if linesBelowCursor > 0 {
		fmt.Fprintf(&sb, ansiCursorUp, linesBelowCursor)
	}

	sb.WriteString("\r")
	if cursorVisualCol > 0 {
		fmt.Fprintf(&sb, ansiCursorForward, cursorVisualCol)
	}

	// Show cursor
	sb.WriteString(ansiShowCursor)

	// Remember where we left the cursor for next render
	d.lastCursorRow = cursorVisualRow
	d.lastLines = totalRows

	d.out.Write([]byte(sb.String()))
}

// GhostRenderState groups the transient values that belong to one ghost frame.
type GhostRenderState struct {
	Streaming bool
	FromAgent bool
	ModelName string
	Status    string
}

// RenderWithGhost draws the buffer with inline ghost text suggestion.
// Ghost text appears after the cursor in dim gray, showing the suggested completion.
// fromAgent indicates whether this is an agent suggestion (show hints) or prediction (fish-style).
//
//nolint:gocyclo // terminal rendering requires many conditional escape sequences
func (d *Display) RenderWithGhost(buf *Buffer, cur *Cursor, hasSelection bool, ghostText string, state GhostRenderState) {
	if d.frame != nil {
		d.renderWithFrame(buf, cur, hasSelection, ghostText, state.Streaming, state.FromAgent, state.ModelName)
		return
	}
	var sb strings.Builder

	// Hide cursor during render for flicker-free updates
	sb.WriteString(ansiHideCursor)

	// Move up from where cursor was left to first line of our content
	if d.lastCursorRow > 0 {
		fmt.Fprintf(&sb, ansiCursorUp, d.lastCursorRow)
	}
	sb.WriteString("\r")

	// Mode indicator: letter + vertical bar with color
	modeBar := ""
	if d.gutter {
		switch d.mode {
		case "normal":
			modeBar = "\x1b[33;1mn│\x1b[0m" // Bold yellow "n│"
		default: // "insert" or empty
			modeBar = "\x1b[2mi│\x1b[0m" // Dim "i│"
		}
	}

	cursorRow := cur.Pos.Row

	for i := 0; i < buf.LineCount(); i++ {
		if i > 0 {
			sb.WriteString("\r\n")
		}
		sb.WriteString(ansiClearLine)

		line := buf.Line(i)

		// Show mode bar on all lines, then prompt on first line
		if d.gutter {
			sb.WriteString(modeBar)
		}
		if i == 0 {
			if d.prompt != "" {
				sb.WriteString(d.prompt)
			} else {
				sb.WriteString(" ")
			}
		} else {
			sb.WriteString(" ")
		}

		// Render line content
		if hasSelection && cur.HasSelection() {
			d.renderLineWithSelection(&sb, line, i, cur)
		} else {
			sb.WriteString(line)
		}

		// Render ghost text on the cursor's line, after the cursor position
		if i == cursorRow && (ghostText != "" || state.Streaming) {
			if ghostText == "" && state.Streaming {
				// Show thinking indicator while waiting for first chunk (agent only)
				// Use consistent text with response_ui states
				if state.Status != "" {
					sb.WriteString("\x1b[90;3m " + state.Status + "\x1b[0m")
				} else {
					sb.WriteString("\x1b[90;3m Agent thinking...\x1b[0m")
				}
			} else if ghostText != "" {
				// Get the first line of ghost text (for single-line display)
				ghostFirstLine := ghostText
				newlineIdx := strings.Index(ghostText, "\n")
				if newlineIdx >= 0 {
					ghostFirstLine = ghostText[:newlineIdx]
				}

				if state.FromAgent {
					// Agent suggestions: dim + italic with hints
					sb.WriteString("\x1b[90;3m") // Dim + italic
					sb.WriteString(ghostFirstLine)
					sb.WriteString(ansiReset)

					// Show streaming indicator if still receiving, otherwise show accept hint
					if state.Streaming {
						sb.WriteString("\x1b[90m▌\x1b[0m")
					} else {
						sb.WriteString("\x1b[90m   [enter]run  [tab]edit  [esc]\x1b[0m")
					}
				} else {
					// Predictions: fish-shell style - just dim gray, no hints
					sb.WriteString("\x1b[38;5;242m") // Gray (brighter than 90)
					sb.WriteString(ghostFirstLine)
					sb.WriteString(ansiReset)
				}
			}
		}
	}

	// Clear everything below the buffer
	sb.WriteString(ansiClearToEnd)

	ghostWidth := renderedGhostSuffixWidth(ghostText, state.Streaming, state.FromAgent)
	totalRows, cursorVisualRow, cursorVisualCol := d.layoutForStandardRender(buf, cur, ghostWidth)
	linesBelowCursor := totalRows - 1 - cursorVisualRow
	if linesBelowCursor > 0 {
		fmt.Fprintf(&sb, ansiCursorUp, linesBelowCursor)
	}

	sb.WriteString("\r")
	if cursorVisualCol > 0 {
		fmt.Fprintf(&sb, ansiCursorForward, cursorVisualCol)
	}

	// Show cursor
	sb.WriteString(ansiShowCursor)

	// Remember where we left the cursor for next render
	d.lastCursorRow = cursorVisualRow
	d.lastLines = totalRows

	d.out.Write([]byte(sb.String()))
}

//nolint:gocyclo // frame rendering requires handling many layout cases sequentially
func (d *Display) renderWithFrame(buf *Buffer, cur *Cursor, hasSelection bool, ghostText string, streaming, fromAgent bool, modelName string) {
	frame := d.frame
	var sb strings.Builder
	traceEnabled := trace.Enabled("editor")

	// Hide cursor during render for flicker-free updates
	sb.WriteString(ansiHideCursor)

	// Move up from where cursor was left to first line of our content
	if d.lastCursorRow > 0 {
		fmt.Fprintf(&sb, ansiCursorUp, d.lastCursorRow)
	}
	sb.WriteString("\r")

	if frame.TopLine != "" {
		d.renderFrameLine(&sb, frame.TopLine, frame.LineBg)
		sb.WriteString("\r\n")
	}
	if frame.LiveTopLine != "" {
		d.renderFrameLine(&sb, frame.LiveTopLine, frame.LineBg)
		sb.WriteString("\r\n")
	}

	cursorRow := cur.Pos.Row
	cursorCol := cur.Pos.Col
	if traceEnabled {
		trace.EditorHigh("frame_render", map[string]any{
			"width":        d.width,
			"height":       d.height,
			"lines":        buf.LineCount(),
			"cursor_row":   cursorRow,
			"cursor_col":   cursorCol,
			"prefix_width": frame.PrefixWidth,
			"line_bg":      frame.LineBg != "",
			"top_line":     frame.TopLine != "",
			"bottom_line":  frame.BottomLine != "",
		})
	}

	for i := 0; i < buf.LineCount(); i++ {
		if i > 0 {
			sb.WriteString("\r\n")
		}
		sb.WriteString(ansiClearLine)
		sb.WriteString(frameRenderPrefix(frame))

		line := buf.Line(i)
		if hasSelection && cur.HasSelection() {
			d.renderLineWithSelection(&sb, line, i, cur)
		} else {
			sb.WriteString(line)
		}

		if traceEnabled {
			trace.EditorDetailed("frame_render_line", map[string]any{
				"index":          i,
				"line_width":     visibleWidth(line),
				"content_width":  frame.PrefixWidth + visibleWidth(line),
				"display_width":  d.width,
				"line_bg_active": frame.LineBg != "",
			})
		}

		// Fill to terminal EOL with the frame background to avoid width mismatches.
		if frame.LineBg != "" {
			fillLineBg(&sb, frame.LineBg)
		}
	}

	if frame.BottomLine != "" {
		if buf.LineCount() > 0 {
			sb.WriteString("\r\n")
		}
		bottomLineBg := frame.LineBg
		if frame.BottomLineBg != "" {
			bottomLineBg = frame.BottomLineBg
		}
		d.renderFrameLine(&sb, frame.BottomLine, bottomLineBg)
	}
	if frame.BottomExtraLine != "" {
		if buf.LineCount() > 0 || frame.BottomLine != "" {
			sb.WriteString("\r\n")
		}
		extraLineBg := frame.LineBg
		if frame.BottomExtraLineBg != "" {
			extraLineBg = frame.BottomExtraLineBg
		}
		d.renderFrameLine(&sb, frame.BottomExtraLine, extraLineBg)
	}

	// Clear everything below the buffer.
	// Use the lowest visible frame row background when available.
	clearBg := frame.LineBg
	if frame.BottomExtraLineBg != "" {
		clearBg = frame.BottomExtraLineBg
	} else if frame.BottomLineBg != "" {
		clearBg = frame.BottomLineBg
	}
	if clearBg != "" {
		sb.WriteString(clearBg)
	}
	sb.WriteString(ansiClearToEnd)
	if clearBg != "" {
		sb.WriteString(ansiReset)
	}
	if traceEnabled {
		trace.EditorDetailed("frame_render_clear", map[string]any{
			"clear_strategy": "line_bg_clear_to_end",
			"display_width":  d.width,
			"line_bg":        frame.LineBg != "",
		})
	}

	totalRows, cursorVisualRow, cursorVisualCol := d.layoutForFrameRender(buf, cur, frame)
	linesBelowCursor := totalRows - 1 - cursorVisualRow
	if linesBelowCursor > 0 {
		fmt.Fprintf(&sb, ansiCursorUp, linesBelowCursor)
	}

	// Position cursor within the line
	sb.WriteString("\r")
	if cursorVisualCol > 0 {
		fmt.Fprintf(&sb, ansiCursorForward, cursorVisualCol)
	}

	// Show cursor
	sb.WriteString(ansiShowCursor)

	// Remember where we left the cursor for next render
	d.lastCursorRow = cursorVisualRow
	d.lastLines = totalRows

	d.out.Write([]byte(sb.String()))
}

func frameRenderPrefix(frame *InputFrame) string {
	if frame.LivePrefix != "" {
		return frame.LivePrefix
	}
	return frame.Prefix
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
	if d.frame != nil {
		d.finalizeWithFrame(buf)
		return
	}
	var sb strings.Builder

	// Move cursor to beginning of the displayed content
	if d.lastCursorRow > 0 {
		fmt.Fprintf(&sb, ansiCursorUp, d.lastCursorRow)
	}
	sb.WriteString("\r")

	// Re-render each line with background highlight
	for i := 0; i < buf.LineCount(); i++ {
		sb.WriteString(ansiClearLine)

		// Apply background color if set
		if d.inputBgCode != "" {
			sb.WriteString(d.inputBgCode)
		}

		var contentWidth int

		// Show gutter bar on all lines (neutral style for finalized)
		if d.gutter {
			sb.WriteString("│")
			contentWidth = 1
		}

		if i == 0 {
			if d.prompt != "" {
				sb.WriteString(d.prompt)
				contentWidth += d.promptWidth
			} else {
				sb.WriteString(" ")
				contentWidth += 1
			}
		} else {
			sb.WriteString(" ")
			contentWidth += 1
		}

		// Bold the command text
		sb.WriteString(ansiBold)
		sb.WriteString(buf.Line(i))
		sb.WriteString(ansiReset)
		contentWidth += visibleWidth(buf.Line(i))

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

func (d *Display) finalizeWithFrame(buf *Buffer) {
	frame := d.frame
	var sb strings.Builder
	previousLines := d.lastLines

	// Move cursor to beginning of the displayed content
	if d.lastCursorRow > 0 {
		fmt.Fprintf(&sb, ansiCursorUp, d.lastCursorRow)
	}
	sb.WriteString("\r")

	linesWritten := 0
	if frame.TopLine != "" {
		d.renderFrameLine(&sb, frame.TopLine, frame.LineBg)
		sb.WriteString("\r\n")
		linesWritten++
	}

	for i := 0; i < buf.LineCount(); i++ {
		sb.WriteString(ansiClearLine)
		sb.WriteString(frame.Prefix)
		sb.WriteString(buf.Line(i))

		if frame.LineBg != "" {
			fillLineBg(&sb, frame.LineBg)
		}

		sb.WriteString("\r\n")
		linesWritten++
	}

	if frame.BottomLine != "" {
		bottomLineBg := frame.LineBg
		if frame.BottomLineBg != "" {
			bottomLineBg = frame.BottomLineBg
		}
		d.renderFrameLine(&sb, frame.BottomLine, bottomLineBg)
		sb.WriteString("\r\n")
		linesWritten++
	}
	if frame.BottomExtraLine != "" {
		extraLineBg := frame.LineBg
		if frame.BottomExtraLineBg != "" {
			extraLineBg = frame.BottomExtraLineBg
		}
		d.renderFrameLine(&sb, frame.BottomExtraLine, extraLineBg)
		sb.WriteString("\r\n")
		linesWritten++
	}

	for i := linesWritten; i < previousLines; i++ {
		sb.WriteString(ansiClearLine)
		if i < previousLines-1 {
			sb.WriteString("\r\n")
		}
	}

	d.out.Write([]byte(sb.String()))
	d.finalizedLines = linesWritten
	d.lastLines = 0
	d.lastCursorRow = 0
}

func (d *Display) renderFrameLine(sb *strings.Builder, line, lineBg string) {
	sb.WriteString(ansiClearLine)
	sb.WriteString(line)

	if lineBg == "" {
		return
	}

	// Fill to end of line with the frame background to avoid off-by-one gaps.
	fillLineBg(sb, lineBg)
}

func fillLineBg(sb *strings.Builder, lineBg string) {
	sb.WriteString(lineBg)
	sb.WriteString("\x1b[K")
	sb.WriteString(ansiReset)
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
	switch {
	case durationMs < 1000:
		text = fmt.Sprintf(" %dms ", durationMs)
	case durationMs < 60000:
		text = fmt.Sprintf(" %.1fs ", float64(durationMs)/1000)
	default:
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
	col := d.width - visibleWidth(text)
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

// truncateToWidth shortens s to at most maxWidth terminal columns, appending
// "..." when something was cut. Width is measured in display cells, not
// runes: CJK characters occupy two columns each.
func truncateToWidth(s string, maxWidth int) string {
	if visibleWidth(s) <= maxWidth {
		return s
	}
	if maxWidth <= 3 {
		return strings.Repeat(".", maxWidth)
	}
	budget := maxWidth - 3
	width := 0
	var b strings.Builder
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if width+rw > budget {
			break
		}
		width += rw
		b.WriteRune(r)
	}
	b.WriteString("...")
	return b.String()
}

// descFixedCols is how many columns a menu row spends on everything except
// the item text and the description: the optional scrollbar column, the space
// before the text, the two-column gap after it, and the trailing space.
func descFixedCols(hasScrollbar bool) int {
	cols := 1 + 2 + 1
	if hasScrollbar {
		cols++
	}
	return cols
}

// RenderCompletionMenu draws the completion dropdown below the cursor.
//
//nolint:gocyclo // completion menu rendering requires layout and scroll calculations
func (d *Display) RenderCompletionMenu(items []CompletionItem, selected, startCol, cursorRow, cursorCol int) {
	if len(items) == 0 {
		return
	}

	var sb strings.Builder

	// Add prefix width to startCol for proper positioning.
	prefixWidth := d.calcPrefixWidth(cursorRow)
	_, menuCol := d.wrappedCursorForChars(startCol + prefixWidth)

	// Calculate max width for alignment. Values are capped to the row budget:
	// a row wider than the terminal wraps onto an extra physical row that the
	// cursor-up below does not account for, corrupting the prompt.
	maxValueWidth := d.width - menuCol - descFixedCols(d.scrollbarCode != "")
	if maxValueWidth < 1 {
		maxValueWidth = 1
	}
	maxTextWidth := 0
	for _, item := range items {
		itemWidth := visibleWidth(item.Text)
		if itemWidth > maxValueWidth {
			itemWidth = maxValueWidth
		}
		if itemWidth > maxTextWidth {
			maxTextWidth = itemWidth
		}
	}

	// Limit visible items (show max 6, scrolling needed for 7+)
	maxVisible := 6
	if len(items) < maxVisible {
		maxVisible = len(items)
	}

	// Calculate scroll offset to keep selected visible
	scrollOffset := 0
	if selected >= maxVisible {
		scrollOffset = selected - maxVisible + 1
	}

	// Draw a colored rail whenever completion menu is visible.
	// For long lists, render a proportional thumb on that rail.
	needsScrollbar := d.scrollbarCode != ""
	scrolling := len(items) > maxVisible
	thumbSize := 1
	thumbStart := 0
	if needsScrollbar && scrolling {
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
	} else if needsScrollbar {
		thumbSize = maxVisible
	}

	// Render menu items
	for i := scrollOffset; i < scrollOffset+maxVisible && i < len(items); i++ {
		item := items[i]
		rowIndex := i - scrollOffset // 0-based index within visible area

		// Move to next line, then clear it and position at menuCol
		sb.WriteString("\r\n")
		sb.WriteString(ansiClearLine)
		if menuCol > 0 {
			fmt.Fprintf(&sb, ansiCursorForward, menuCol)
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
		text := truncateToWidth(item.Text, maxValueWidth)
		sb.WriteString(" ")
		sb.WriteString(text)

		// Pad to align descriptions
		padding := maxTextWidth - visibleWidth(text) + 2
		for j := 0; j < padding; j++ {
			sb.WriteByte(' ')
		}

		// Description (dimmed) - only render if we have room.
		//
		// The budget must be measured from menuCol, where the row actually
		// starts, not from startCol: menuCol includes the prompt/gutter prefix.
		// A row wider than the terminal wraps onto an extra physical row, while
		// the cursor-up below only accounts for logical rows, so the cursor ends
		// up below the input line and the next render stacks a duplicate prompt.
		if item.Description != "" {
			maxDesc := d.width - menuCol - maxTextWidth - descFixedCols(needsScrollbar)
			if maxDesc > 3 { // Only render if we have room for at least some text
				if i != selected {
					sb.WriteString("\x1b[2m") // Dim
				}
				// Truncate by display width, not rune count: CJK runes
				// occupy two columns and would overflow the row.
				sb.WriteString(truncateToWidth(item.Description, maxDesc))
				if i != selected {
					sb.WriteString(ansiReset)
				}
			}
		}

		sb.WriteString(" ")

		if i == selected {
			sb.WriteString(ansiReset)
		}
	}

	// Return to the original input cursor position explicitly instead of relying
	// on terminal-specific cursor save/restore behavior.
	fmt.Fprintf(&sb, ansiCursorUp, maxVisible)
	sb.WriteString("\r")
	_, restoreCol := d.wrappedCursorForChars(cursorCol + prefixWidth)
	if restoreCol > 0 {
		fmt.Fprintf(&sb, ansiCursorForward, restoreCol)
	}

	d.out.Write([]byte(sb.String()))

	// Track menu size for clearing on next render
	d.lastMenuItems = len(items)
}

// RenderCompletionNotice draws a one-line dim notice below the input where the
// completion menu would appear ("no matches", "completion timed out").
// It reuses the menu clearing contract via lastMenuItems.
func (d *Display) RenderCompletionNotice(notice string, cursorRow, cursorCol int) {
	if notice == "" {
		return
	}

	var sb strings.Builder

	prefixWidth := d.calcPrefixWidth(cursorRow)

	sb.WriteString("\r\n")
	sb.WriteString(ansiClearLine)
	sb.WriteString("\x1b[2m") // Dim
	sb.WriteString("  ")
	sb.WriteString(notice)
	sb.WriteString(ansiReset)

	// Return to the original input cursor position.
	fmt.Fprintf(&sb, ansiCursorUp, 1)
	sb.WriteString("\r")
	_, restoreCol := d.wrappedCursorForChars(cursorCol + prefixWidth)
	if restoreCol > 0 {
		fmt.Fprintf(&sb, ansiCursorForward, restoreCol)
	}

	d.out.Write([]byte(sb.String()))

	// Track as one menu row so the next render clears it
	d.lastMenuItems = 1
}

// ClearCompletionMenu removes the completion menu from display.
func (d *Display) ClearCompletionMenu(numItems, cursorRow, cursorCol int) {
	if numItems == 0 {
		return
	}

	var sb strings.Builder

	// Move through the already-rendered menu rows and clear them. Do not emit
	// CRLF here: when the prompt is near the terminal bottom, newlines can
	// scroll the viewport and leave the editor cursor out of sync after Esc.
	maxVisible := 6
	if numItems < maxVisible {
		maxVisible = numItems
	}

	for i := 0; i < maxVisible; i++ {
		fmt.Fprintf(&sb, ansiCursorDown, 1)
		sb.WriteString("\r")
		sb.WriteString(ansiClearLine)
	}

	// Move back to where the input cursor was before clearing.
	fmt.Fprintf(&sb, ansiCursorUp, maxVisible)
	sb.WriteString("\r")
	prefixWidth := d.calcPrefixWidth(cursorRow)
	_, restoreCol := d.wrappedCursorForChars(cursorCol + prefixWidth)
	if restoreCol > 0 {
		fmt.Fprintf(&sb, ansiCursorForward, restoreCol)
	}

	d.out.Write([]byte(sb.String()))

	// Reset tracked menu size
	d.lastMenuItems = 0
}

// RenderPermissionPrompt draws the permission request UI.
// Returns after drawing - caller should handle input and then call ClearPermissionPrompt.
//
// Deprecated: Use AgentOutputCoordinator.RenderPermissionPrompt instead for
// proper coordination with streaming output.
func (d *Display) RenderPermissionPrompt(command, accentColor string) {
	var sb strings.Builder

	// Build accent color ANSI code
	accentCode := ""
	if accentColor != "" && len(accentColor) == 7 && accentColor[0] == '#' {
		var r, g, b int
		fmt.Sscanf(accentColor[1:], "%02x%02x%02x", &r, &g, &b) //nolint:errcheck
		accentCode = fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
	} else {
		accentCode = "\x1b[36m" // Fallback to cyan
	}

	// Render the prompt box with colored bar and background
	// Use bold + color for high visibility
	barStyle := accentCode + ansiBold
	lines := []string{
		"",
		barStyle + "│" + ansiReset + " " + ansiBold + "Agent wants to run:" + ansiReset,
		barStyle + "│" + ansiReset + " " + accentCode + ansiBold + command + ansiReset,
		barStyle + "│" + ansiReset,
		barStyle + "│" + ansiReset + " [y]allow  [n]deny  [a]always allow",
	}

	for _, line := range lines {
		sb.WriteString("\r\n")
		sb.WriteString(ansiClearLine)
		sb.WriteString(line)
	}

	d.out.Write([]byte(sb.String()))
}

// ClearPermissionPrompt removes the permission prompt from display.
//
// Deprecated: Use AgentOutputCoordinator.ClearPermissionPrompt instead for
// proper coordination with streaming output.
func (d *Display) ClearPermissionPrompt() {
	var sb strings.Builder

	// Move up 5 lines and clear each
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&sb, ansiCursorUp, 1)
		sb.WriteString("\r")
		sb.WriteString(ansiClearLine)
	}

	d.out.Write([]byte(sb.String()))
}
