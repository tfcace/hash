// internal/editor/display.go
package editor

import (
	"fmt"
	"io"
	"strings"

	"github.com/mattn/go-runewidth"
	"github.com/tfcace/hash/internal/termtext"
	"github.com/tfcace/hash/internal/trace"
)

// ANSI escape sequences
const (
	ansiClearLine     = "\x1b[2K"
	ansiCursorUp      = "\x1b[%dA"
	ansiCursorDown    = "\x1b[%dB"
	ansiCursorForward = "\x1b[%dC"
	ansiReverse       = "\x1b[7m"
	ansiReset         = "\x1b[0m"
	ansiHideCursor    = "\x1b[?25l"
	ansiShowCursor    = "\x1b[?25h"
	ansiClearToEnd    = "\x1b[J"
	ansiBold          = "\x1b[1m"
)

// Display handles terminal rendering.
type Display struct {
	out           io.Writer
	width         int
	view          viewport
	gutter        bool
	normalMode    bool   // Editor is in normal mode (gutter shows "n")
	prompt        string // Prompt to display before first line
	promptWidth   int
	drawn         drawnFrame
	inputBgCode   string // ANSI code for input background color
	scrollbarCode string // ANSI code for scrollbar foreground color
	frame         *InputFrame
}

// drawnFrame is what the last draw left on the terminal. The next draw and
// every clear work from it, not from editor state that has moved on since
// (a clear after an edit sees the new cursor and the new menu items).
type drawnFrame struct {
	cursorRow int // the cursor's visual row, counted from the first input row
	cursorCol int // the cursor's column in cells, as positioned after a \r
	rows      int // the input's visual rows; a menu or notice goes below them
	below     int // rows drawn under the input: a completion menu or notice
}

// viewport is the part of the input's visual rows a frame draws: at most
// the pane's height of rows less the ones the frame draws under the input,
// from top. An input taller than that scrolls inside it, following the
// cursor, instead of every frame running its top rows into scrollback and
// moving up more rows than the screen has; one that fits is drawn whole,
// from its first row.
type viewport struct {
	top    int // the first visual row of the input drawn
	height int // the pane's rows; 0 when unknown: the input is drawn whole
	under  int // rows the frame draws under the input (a menu or notice)
}

// shown is how many input rows the viewport holds: the pane's rows less
// the ones under the input, at least one.
func (v *viewport) shown() int {
	return max(v.height-v.under, 1)
}

// follow moves the viewport the least that keeps row, the cursor's visual
// row of an input rows tall, on screen. It reports whether the input is
// cut: taller than the rows the pane leaves it.
func (v *viewport) follow(row, rows int) bool {
	if v.height <= 0 || rows <= v.shown() {
		v.top = 0
		return false
	}
	v.top = min(max(v.top, row-v.shown()+1), row, rows-v.shown())
	v.top = max(v.top, 0)
	return true
}

// rowsBelowCursor is how many input rows were drawn under the cursor's row.
func (f drawnFrame) rowsBelowCursor() int {
	return max(f.rows-1-f.cursorRow, 0)
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
		out:   out,
		width: width,
		view:  viewport{height: height},
	}
}

// SetGutter enables/disables the gutter indicator.
func (d *Display) SetGutter(enabled bool) {
	d.gutter = enabled
}

// SetNormalMode sets whether the gutter shows normal (true) or insert mode.
func (d *Display) SetNormalMode(normal bool) {
	d.normalMode = normal
}

// modeBar returns the gutter's mode indicator, or "" without a gutter.
// Insert mode: dim "i│" (unobtrusive, normal typing)
// Normal mode: bold yellow "n│" (attention, command mode)
func (d *Display) modeBar() string {
	switch {
	case !d.gutter:
		return ""
	case d.normalMode:
		return "\x1b[33;1mn│\x1b[0m"
	default:
		return "\x1b[2mi│\x1b[0m"
	}
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

// lineCells is where byte col of the logical line row ends, in wrapped
// cells after its prefix (see wrapCells).
func (d *Display) lineCells(row int, line string, col int) int {
	return lineCells(d, row, line, col)
}

// WrapWidth implements LineLayout: the width used for soft wrapping.
func (d *Display) WrapWidth() int {
	return d.wrapWidth()
}

// PrefixWidth implements LineLayout: the visible width rendered before a
// logical line.
func (d *Display) PrefixWidth(row int) int {
	return d.calcPrefixWidth(row)
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

// visibleWidthAtByteIndex is how many cells the text of s before byte col
// takes when drawn.
func visibleWidthAtByteIndex(s string, col int) int {
	col = clampByteIndexToRuneBoundary(s, col)
	return termtext.Width(s[:col])
}

// inlineAcceptHint follows a finished inline ?? suggestion, in the key
// vocabulary hash uses for every hint.
const inlineAcceptHint = "   enter to run · tab to edit · esc to cancel"

// inlineReviewHint follows a finished fill with lines not drawn: Enter puts
// it in the buffer to read before it can run.
const inlineReviewHint = " · enter to review · esc to cancel"

// ghostSuffix is what is drawn after the cursor's line: the ghost text's
// first line with its hint or streaming mark, the agent's status while
// nothing has arrived, or why the agent fill failed. It returns the suffix
// as drawn and the text it shows (ANSI excluded), which layout wraps after
// the line as the terminal does, so both come from one place.
func ghostSuffix(ghostText string, state GhostRenderState) (drawn, shown string) {
	const dimItalic, dim = "\x1b[90;3m", "\x1b[90m"
	if ghostText == "" {
		switch {
		case !state.Streaming && state.Notice != "":
			// The agent ghost ended with an error: say why, where the fill would have been
			shown = " " + termtext.Inert(state.Notice)
		case !state.Streaming:
			return "", ""
		case state.Status != "":
			shown = " " + termtext.Inert(state.Status)
		default:
			// Waiting for the first chunk, in the same words as the response UI
			shown = " Agent thinking..."
		}
		return dimItalic + shown + ansiReset, shown
	}

	// Only the ghost's first line shows (single-line display); an agent
	// fill says how many lines follow it.
	first, hidden := ghostShown(ghostText)
	first = termtext.Inert(first)
	if !state.FromAgent {
		// Predictions: fish-shell style, just dim gray, no hints
		return "\x1b[38;5;242m" + first + ansiReset, first
	}
	// Agent suggestions: dim + italic, then the streaming mark or the hint
	more := ""
	if hidden > 0 {
		more = fmt.Sprintf("  +%d lines", hidden)
		if hidden == 1 {
			more = "  +1 line"
		}
	}
	var tail string
	switch {
	case state.Streaming:
		tail = more + "▌"
	case hidden > 0:
		tail = more + inlineReviewHint
	default:
		tail = inlineAcceptHint
	}
	return dimItalic + first + ansiReset + dim + tail + ansiReset, first + tail
}

// layoutForStandardRender computes visual cursor/line positions for wrapped input.
// suffix is the text drawn after the cursor's line (a ghost), wrapped after
// it as the terminal wraps it.
func (d *Display) layoutForStandardRender(buf *Buffer, cur *Cursor, suffix string) (totalRows, cursorVisualRow, cursorVisualCol int) {
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
		lineWidth := wrapCells(d.wrapWidth(), prefixWidth, line, len(line))

		if i == cursorRow {
			cursorChars := wrapCells(d.wrapWidth(), prefixWidth, line, cursorCol)
			rowOffset, col := d.wrappedCursorForChars(cursorChars)
			cursorVisualRow = totalRows + rowOffset
			cursorVisualCol = col
			lineWidth = wrapCells(d.wrapWidth(), lineWidth, suffix, len(suffix))
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
		lineWidth := wrapCells(d.wrapWidth(), frame.PrefixWidth, line, len(line))

		if i == cursorRow {
			cursorChars := wrapCells(d.wrapWidth(), frame.PrefixWidth, line, cursorCol)
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
	d.RenderWithGhost(buf, cur, hasSelection, "", GhostRenderState{})
}

// GhostRenderState groups the transient values that belong to one ghost frame.
type GhostRenderState struct {
	Streaming bool
	FromAgent bool
	Status    string
	Notice    string // shown after the cursor when an agent ghost ended with an error
}

// RenderWithGhost draws the buffer with inline ghost text suggestion.
// Ghost text appears after the cursor in dim gray, showing the suggested completion.
// fromAgent indicates whether this is an agent suggestion (show hints) or prediction (fish-style).
//
//nolint:gocyclo // terminal rendering requires many conditional escape sequences
func (d *Display) RenderWithGhost(buf *Buffer, cur *Cursor, hasSelection bool, ghostText string, state GhostRenderState) {
	if d.frame != nil {
		// The framed reply box has no ghost source (no predictions, no
		// inline agent stream), so it renders no ghost text or notices.
		d.renderWithFrame(buf, cur, hasSelection)
		return
	}
	suffix, shown := ghostSuffix(ghostText, state)
	totalRows, cursorVisualRow, cursorVisualCol := d.layoutForStandardRender(buf, cur, shown)
	if d.view.follow(cursorVisualRow, totalRows) {
		d.renderViewport(buf, cur, hasSelection, suffix, cursorVisualRow, cursorVisualCol)
		return
	}
	var sb strings.Builder

	// Hide cursor during render for flicker-free updates
	sb.WriteString(ansiHideCursor)

	// Move up from where cursor was left to first line of our content
	if d.drawn.cursorRow > 0 {
		fmt.Fprintf(&sb, ansiCursorUp, d.drawn.cursorRow)
	}
	sb.WriteString("\r")

	for i := 0; i < buf.LineCount(); i++ {
		if i > 0 {
			sb.WriteString("\r\n")
		}
		sb.WriteString(ansiClearLine)
		d.drawLine(&sb, buf, cur, i, hasSelection, suffix)
	}

	// Clear everything below the buffer
	sb.WriteString(ansiClearToEnd)

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

	// Remember what is on screen for the next draw (the clear to end took any menu)
	d.drawn = drawnFrame{cursorRow: cursorVisualRow, cursorCol: cursorVisualCol, rows: totalRows}

	d.out.Write([]byte(sb.String()))
}

// drawLine draws logical line i as the terminal wraps it: the mode bar,
// the prompt (line 0) or a space, the text (its selection reversed), and
// suffix, the ghost, after the cursor's line.
func (d *Display) drawLine(sb *strings.Builder, buf *Buffer, cur *Cursor, i int, hasSelection bool, suffix string) {
	sb.WriteString(d.modeBar())
	if i == 0 && d.prompt != "" {
		sb.WriteString(d.prompt)
	} else {
		sb.WriteString(" ")
	}
	if hasSelection && cur.HasSelection() {
		d.renderLineWithSelection(sb, buf, i, cur)
	} else {
		sb.WriteString(termtext.Inert(buf.Line(i)))
	}
	if i == cur.Pos.Row {
		sb.WriteString(suffix)
	}
}

// renderViewport draws the rows of an input taller than the pane that the
// viewport shows (follow placed it around the cursor's visual row): each
// line is cut into the rows the terminal would wrap it into, and only the
// viewport's are written, so the frame never scrolls the screen.
func (d *Display) renderViewport(buf *Buffer, cur *Cursor, hasSelection bool, suffix string, cursorRow, cursorCol int) {
	var rows []string
	for i := 0; i < buf.LineCount(); i++ {
		var line strings.Builder
		d.drawLine(&line, buf, cur, i, hasSelection, suffix)
		rows = append(rows, wrapRows(line.String(), d.wrapWidth())...)
	}
	top := min(d.view.top, len(rows))
	shown := rows[top:min(top+d.view.shown(), len(rows))]

	var sb strings.Builder
	sb.WriteString(ansiHideCursor)
	if d.drawn.cursorRow > 0 {
		fmt.Fprintf(&sb, ansiCursorUp, d.drawn.cursorRow)
	}
	// Clear first: an ED written after a full last row would take its last
	// cell (the cursor waits there to wrap).
	sb.WriteString("\r" + ansiClearToEnd)
	for k, row := range shown {
		if k > 0 {
			sb.WriteString(ansiReset + "\r\n")
		}
		sb.WriteString(row)
	}
	sb.WriteString(ansiReset)

	row := cursorRow - top
	if below := len(shown) - 1 - row; below > 0 {
		fmt.Fprintf(&sb, ansiCursorUp, below)
	}
	sb.WriteString("\r")
	if cursorCol > 0 {
		fmt.Fprintf(&sb, ansiCursorForward, cursorCol)
	}
	sb.WriteString(ansiShowCursor)
	d.drawn = drawnFrame{cursorRow: row, cursorCol: cursorCol, rows: len(shown)}
	d.out.Write([]byte(sb.String()))
}

// wrapRows cuts s, a drawn line, into the rows the terminal wraps it into
// at width cells (a wide character that does not fit goes whole to the
// next row, as nextCell lays it out). Each row starts with the colors in
// effect where it starts, so any row can be drawn without the ones before.
func wrapRows(s string, width int) []string {
	var rows []string
	var row, colors strings.Builder
	cells := 0
	for i := 0; i < len(s); {
		if n := termtext.EscapeLen(s[i:]); n > 0 {
			seq := s[i : i+n]
			row.WriteString(seq)
			switch {
			case seq == ansiReset || seq == "\x1b[m":
				colors.Reset()
			case strings.HasPrefix(seq, "\x1b[") && strings.HasSuffix(seq, "m"):
				colors.WriteString(seq)
			}
			i += n
			continue
		}
		end := termtext.ClusterEnd(s, i)
		c := s[i:end]
		w := termtext.Width(c)
		if w > 0 && cells > 0 && cells+w > width {
			rows = append(rows, row.String())
			row.Reset()
			row.WriteString(colors.String())
			cells = 0
		}
		cells += w
		row.WriteString(c)
		i = end
	}
	return append(rows, row.String())
}

//nolint:gocyclo // frame rendering requires handling many layout cases sequentially
func (d *Display) renderWithFrame(buf *Buffer, cur *Cursor, hasSelection bool) {
	frame := d.frame
	var sb strings.Builder
	traceEnabled := trace.Enabled("editor")

	// Hide cursor during render for flicker-free updates
	sb.WriteString(ansiHideCursor)

	// Move up from where cursor was left to first line of our content
	if d.drawn.cursorRow > 0 {
		fmt.Fprintf(&sb, ansiCursorUp, d.drawn.cursorRow)
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
			"height":       d.view.height,
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
			d.renderLineWithSelection(&sb, buf, i, cur)
		} else {
			sb.WriteString(termtext.Inert(line))
		}

		if traceEnabled {
			trace.EditorDetailed("frame_render_line", map[string]any{
				"index":          i,
				"line_width":     termtext.Width(line),
				"content_width":  frame.PrefixWidth + termtext.Width(line),
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

	// Remember what is on screen for the next draw (the clear to end took any menu)
	d.drawn = drawnFrame{cursorRow: cursorVisualRow, cursorCol: cursorVisualCol, rows: totalRows}

	d.out.Write([]byte(sb.String()))
}

func frameRenderPrefix(frame *InputFrame) string {
	if frame.LivePrefix != "" {
		return frame.LivePrefix
	}
	return frame.Prefix
}

func (d *Display) renderLineWithSelection(sb *strings.Builder, buf *Buffer, row int, cur *Cursor) {
	line := buf.Line(row)
	start, end := cur.SelectionRange(buf)

	// Check if this line has any selection
	if row < start.Row || row > end.Row {
		sb.WriteString(termtext.Inert(line))
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
	sb.WriteString(termtext.Inert(line[:startCol]))
	sb.WriteString(ansiReverse)
	sb.WriteString(termtext.Inert(line[startCol:endCol]))
	sb.WriteString(ansiReset)
	sb.WriteString(termtext.Inert(line[endCol:]))
}

// Clear clears the display area.
func (d *Display) Clear() {
	if d.drawn.cursorRow > 0 {
		fmt.Fprintf(d.out, ansiCursorUp, d.drawn.cursorRow)
	}
	fmt.Fprint(d.out, "\r")
	fmt.Fprint(d.out, ansiClearToEnd)
	d.drawn = drawnFrame{}
}

// Leave moves from the cursor past the input's last row to the start of a
// new row, leaving the input as drawn.
func (d *Display) Leave() {
	var sb strings.Builder
	d.belowInput(&sb)
	sb.WriteString("\r\n")
	d.drawn = drawnFrame{}
	d.out.Write([]byte(sb.String()))
}

// Finalize leaves the content on screen and moves to a new line.
// Re-renders with background highlight to distinguish from output.
func (d *Display) Finalize(buf *Buffer) {
	if d.frame != nil {
		d.finalizeWithFrame(buf)
		return
	}
	var sb strings.Builder

	// Move cursor to beginning of the displayed content and clear every row
	// the live render used: the redraw's prefix differs, so a wrapped line
	// lands on different columns and per-line clears would leave old tails.
	if d.drawn.cursorRow > 0 {
		fmt.Fprintf(&sb, ansiCursorUp, d.drawn.cursorRow)
	}
	sb.WriteString("\r")
	sb.WriteString(ansiClearToEnd)

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
		sb.WriteString(termtext.Inert(buf.Line(i)))
		sb.WriteString(ansiReset)
		contentWidth += termtext.Width(buf.Line(i))

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
	d.drawn = drawnFrame{}
}

func (d *Display) finalizeWithFrame(buf *Buffer) {
	frame := d.frame
	var sb strings.Builder

	// Move cursor to beginning of the displayed content and clear every row
	// the live render used (its live top line and prefix differ from the
	// committed ones, so rows shift).
	if d.drawn.cursorRow > 0 {
		fmt.Fprintf(&sb, ansiCursorUp, d.drawn.cursorRow)
	}
	sb.WriteString("\r")
	sb.WriteString(ansiClearToEnd)

	if frame.TopLine != "" {
		d.renderFrameLine(&sb, frame.TopLine, frame.LineBg)
		sb.WriteString("\r\n")
	}

	for i := 0; i < buf.LineCount(); i++ {
		sb.WriteString(ansiClearLine)
		sb.WriteString(frame.Prefix)
		sb.WriteString(termtext.Inert(buf.Line(i)))

		if frame.LineBg != "" {
			fillLineBg(&sb, frame.LineBg)
		}

		sb.WriteString("\r\n")
	}

	if frame.BottomLine != "" {
		bottomLineBg := frame.LineBg
		if frame.BottomLineBg != "" {
			bottomLineBg = frame.BottomLineBg
		}
		d.renderFrameLine(&sb, frame.BottomLine, bottomLineBg)
		sb.WriteString("\r\n")
	}
	if frame.BottomExtraLine != "" {
		extraLineBg := frame.LineBg
		if frame.BottomExtraLineBg != "" {
			extraLineBg = frame.BottomExtraLineBg
		}
		d.renderFrameLine(&sb, frame.BottomExtraLine, extraLineBg)
		sb.WriteString("\r\n")
	}

	d.out.Write([]byte(sb.String()))
	d.drawn = drawnFrame{}
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

// SetRowsUnder says how many rows the next frame draws under the input (a
// completion menu or notice, 0 for none), so the input's viewport leaves
// them room in the pane: written past its bottom, they would scroll the
// input into scrollback and the cursor's row off the screen.
func (d *Display) SetRowsUnder(rows int) {
	d.view.under = rows
}

// Resize updates the terminal dimensions.
func (d *Display) Resize(width, height int) {
	d.width = width
	d.view.height = height
}

// CompletionItem is passed to display for rendering.
type CompletionItem struct {
	Text        string
	Description string
}

// truncateToWidth shortens s to at most maxWidth terminal columns, appending
// "..." when something was cut. Width is measured in display cells by
// grapheme cluster, never splitting one.
func truncateToWidth(s string, maxWidth int) string {
	if visibleWidth(s) <= maxWidth {
		return s
	}
	if maxWidth <= 3 {
		return strings.Repeat(".", maxWidth)
	}
	return termtext.Truncate(s, maxWidth, "...")
}

// displayItems is items as drawn (see termtext.Inert).
func displayItems(items []CompletionItem) []CompletionItem {
	out := make([]CompletionItem, len(items))
	for i, item := range items {
		out[i] = CompletionItem{Text: termtext.Inert(item.Text), Description: termtext.Inert(item.Description)}
	}
	return out
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

// menuRows is how many completion items show at once.
const menuRows = 6

// completionMenuRows is how many rows RenderCompletionMenu draws under the
// input for items items and a note.
func completionMenuRows(items int, note string) int {
	rows := min(items, menuRows)
	if note != "" {
		rows++
	}
	return rows
}

// RenderCompletionMenu draws the completion dropdown below the input.
//
// The menu opens under the completed word: wordCell is where it starts in
// its line (row), in wrapped cells after the prefix (lineCells). A note, if
// any, says on a dim row under the items what the menu left out. The cursor
// goes back to where the last input render left it.
//
//nolint:gocyclo // completion menu rendering requires layout and scroll calculations
func (d *Display) RenderCompletionMenu(items []CompletionItem, selected, row, wordCell int, note string) {
	if len(items) == 0 {
		return
	}

	var sb strings.Builder
	d.belowInput(&sb)

	// Candidates come from filenames and other tools: draw them inert.
	items = displayItems(items)
	longest := 0
	for _, item := range items {
		longest = max(longest, visibleWidth(item.Text))
	}

	// The menu opens under the word: its cell in the word's wrapped row
	// (a word at a row's first cell is at 0). Rows are capped to the
	// terminal: a row wider than it wraps onto an extra physical row that
	// the cursor-up below does not account for, corrupting the prompt. A
	// word too near the right edge for its items moves the menu left, so
	// they show in full while the terminal is wide enough.
	fixed := descFixedCols(d.scrollbarCode != "")
	menuCol := (wordCell + d.calcPrefixWidth(row)) % d.wrapWidth()
	menuCol = max(min(menuCol, d.width-fixed-min(longest, d.width-fixed)), 0)
	maxValueWidth := max(d.width-menuCol-fixed, 1)
	maxTextWidth := min(longest, maxValueWidth)

	// Limit visible items (scrolling past menuRows)
	maxVisible := min(len(items), menuRows)

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

	if note != "" {
		// One row, or backToCursor would come back short of the input.
		sb.WriteString("\r\n")
		sb.WriteString(ansiClearLine)
		if menuCol > 0 {
			fmt.Fprintf(&sb, ansiCursorForward, menuCol)
		}
		sb.WriteString("\x1b[2m") // Dim
		sb.WriteString(termtext.Truncate("  "+note, max(d.width-menuCol-1, 0), "…"))
		sb.WriteString(ansiReset)
	}
	d.drawn.below = completionMenuRows(len(items), note)
	d.backToCursor(&sb)
	d.out.Write([]byte(sb.String()))
}

// belowInput moves from the cursor to the input's last row, where rows
// drawn under the input start (each with a \r\n). The rows passed exist, so
// moving over them never scrolls.
func (d *Display) belowInput(sb *strings.Builder) {
	if below := d.drawn.rowsBelowCursor(); below > 0 {
		fmt.Fprintf(sb, ansiCursorDown, below)
	}
}

// backToCursor returns from the rows drawn under the input to the cursor,
// explicitly rather than with terminal-specific save/restore. The next input
// render clears them with everything below the input.
func (d *Display) backToCursor(sb *strings.Builder) {
	fmt.Fprintf(sb, ansiCursorUp, d.drawn.below+d.drawn.rowsBelowCursor())
	sb.WriteString("\r")
	if d.drawn.cursorCol > 0 {
		fmt.Fprintf(sb, ansiCursorForward, d.drawn.cursorCol)
	}
}

// RenderCompletionNotice draws a one-line dim notice below the input where the
// completion menu would appear ("no matches", "completion timed out").
func (d *Display) RenderCompletionNotice(notice string) {
	if notice == "" {
		return
	}

	var sb strings.Builder
	d.belowInput(&sb)
	sb.WriteString("\r\n")
	sb.WriteString(ansiClearLine)
	sb.WriteString("\x1b[2m") // Dim
	// One row, or backToCursor would come back short of the input.
	sb.WriteString(termtext.Truncate("  "+notice, max(d.wrapWidth()-1, 0), "…"))
	sb.WriteString(ansiReset)

	d.drawn.below = 1
	d.backToCursor(&sb)
	d.out.Write([]byte(sb.String()))
}
