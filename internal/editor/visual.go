package editor

import "github.com/tfcace/hash/internal/termtext"

// LineLayout exposes the display's wrap geometry to cursor motion so
// vertical movement can operate on visual (soft-wrapped) rows.
type LineLayout interface {
	// WrapWidth is the terminal width used for soft wrapping.
	WrapWidth() int
	// PrefixWidth is the visible width rendered before a logical line
	// (prompt on row 0, gutter/space on continuation rows).
	PrefixWidth(row int) int
}

// wrapCells is where byte upto of line ends, in cells of rows width wide
// laid end to end, when the line is drawn after prefix cells. A wide
// character that does not fit in its row's last cell is drawn at the start
// of the next row, as terminals do, and the cell it skips counts. A tab or
// a control character is drawn as narrow cells (spaces, caret notation)
// that wrap one at a time.
func wrapCells(width, prefix int, line string, upto int) int {
	cells := prefix
	upto = min(upto, len(line))
	for i := 0; i < upto; {
		if plainASCIICell(line, i) {
			cells++ // one narrow cell: never pushed, no segmentation needed
			i++
			continue
		}
		var end int
		end, cells = nextCell(width, cells, line, i)
		i = end
	}
	return cells
}

// plainASCIICell reports whether byte i of line is a printable ASCII
// character that is a cluster of its own: the next byte is ASCII too (or
// there is none), so no combining mark or joiner follows it.
func plainASCIICell(line string, i int) bool {
	b := line[i]
	return b >= 0x20 && b < 0x7f && (i+1 == len(line) || line[i+1] < 0x80)
}

// nextCell steps over the cluster at byte i of line, drawn at cell cells:
// where the cluster ends, and the cell after it.
func nextCell(width, cells int, line string, i int) (end, after int) {
	end = termtext.ClusterEnd(line, i)
	c := line[i:end]
	w := termtext.Width(c)
	// Only a glyph drawn as itself is pushed whole to the next row.
	if left := width - cells%width; w > left && w <= width && cells%width != 0 && termtext.Inert(c) == c {
		cells += left
	}
	return end, cells + w
}

// lineCells is where byte col of the logical line row ends, in wrapped
// cells counted from the line's first cell after its prefix.
func lineCells(layout LineLayout, row int, line string, col int) int {
	prefix := layout.PrefixWidth(row)
	return wrapCells(layout.WrapWidth(), prefix, line, col) - prefix
}

// visualUp moves the cursor up one visual row, remembering the goal column.
// Returns false when the cursor is already on the buffer's first visual row
// (the caller then navigates history instead).
func visualUp(state *EditorState) bool {
	layout := state.Layout
	if layout == nil {
		return logicalUp(state)
	}

	row, col := state.Cursor.Pos.Row, state.Cursor.Pos.Col
	line := state.Buffer.Line(row)
	width := layout.WrapWidth()
	abs := wrapCells(width, layout.PrefixWidth(row), line, col)
	vr, vcol := visualPos(abs, width)
	goal := state.currentGoal(vcol)

	if vr > 0 {
		newCol := byteColForAbs(layout, line, row, (vr-1)*width+goal)
		if newCol != col {
			state.Cursor.Pos.Col = newCol
			state.rememberGoal(goal)
			return true
		}
		// No movement: the rows above hold only the line's prefix (a prompt
		// wider than the terminal). The cursor is at the line's first
		// character, so fall through to the line/buffer boundary cases.
	}
	if row > 0 {
		prevLine := state.Buffer.Line(row - 1)
		lastVR := lastVisualRow(layout, prevLine, row-1)
		state.Cursor.Pos.Row = row - 1
		state.Cursor.Pos.Col = byteColForAbs(layout, prevLine, row-1, lastVR*width+goal)
		state.rememberGoal(goal)
		return true
	}
	return false
}

// visualDown mirrors visualUp for downward movement.
func visualDown(state *EditorState) bool {
	layout := state.Layout
	if layout == nil {
		return logicalDown(state)
	}

	row, col := state.Cursor.Pos.Row, state.Cursor.Pos.Col
	line := state.Buffer.Line(row)
	width := layout.WrapWidth()
	abs := wrapCells(width, layout.PrefixWidth(row), line, col)
	vr, vcol := visualPos(abs, width)
	goal := state.currentGoal(vcol)

	switch {
	case vr < lastVisualRow(layout, line, row):
		state.Cursor.Pos.Col = byteColForAbs(layout, line, row, (vr+1)*width+goal)
	case row < state.Buffer.LineCount()-1:
		state.Cursor.Pos.Row = row + 1
		state.Cursor.Pos.Col = byteColForAbs(layout, state.Buffer.Line(row+1), row+1, goal)
	default:
		return false
	}
	state.rememberGoal(goal)
	return true
}

// currentGoal returns the remembered goal column if the cursor has not moved
// since it was set, else the fallback (the cursor's current screen column).
func (s *EditorState) currentGoal(fallback int) int {
	if g := s.goal; g != nil && g.at == s.Cursor.Pos {
		return g.col
	}
	return fallback
}

// rememberGoal stores the goal column against the cursor's new position.
func (s *EditorState) rememberGoal(goal int) {
	s.goal = &verticalGoal{col: goal, at: s.Cursor.Pos}
}

// visualPos maps an absolute screen column to the visual row and column the
// renderer draws it at. A cursor sitting exactly on a wrap boundary belongs
// to the end of the previous row (pending wrap), matching
// Display.wrappedCursorForChars, so vcol ranges over 0..width inclusive.
func visualPos(abs, width int) (vr, vcol int) {
	if abs <= 0 {
		return 0, 0
	}
	vr = (abs - 1) / width
	return vr, abs - vr*width
}

// lastVisualRow returns the last visual row offset of the logical line.
func lastVisualRow(layout LineLayout, line string, row int) int {
	total := wrapCells(layout.WrapWidth(), layout.PrefixWidth(row), line, len(line))
	if total <= 0 {
		return 0
	}
	return (total - 1) / layout.WrapWidth()
}

// byteColForAbs returns the byte column in line whose visual position is
// closest to, but not past, absolute screen column target. It steps over
// grapheme clusters, measured as the display draws them: a wide character
// or a joined emoji is never split, and a target inside one lands before it.
func byteColForAbs(layout LineLayout, line string, row, target int) int {
	width, cells := layout.WrapWidth(), layout.PrefixWidth(row)
	col := 0
	for col < len(line) {
		end, after := nextCell(width, cells, line, col)
		if after > target {
			break
		}
		col, cells = end, after
	}
	return col
}

// logicalUp is the vertical fallback when no layout is available: move by
// logical line, clamping the column.
func logicalUp(state *EditorState) bool {
	if state.Cursor.Pos.Row == 0 {
		return false
	}
	state.Cursor.Pos.Row--
	clampColToLine(state)
	return true
}

// logicalDown mirrors logicalUp.
func logicalDown(state *EditorState) bool {
	if state.Cursor.Pos.Row >= state.Buffer.LineCount()-1 {
		return false
	}
	state.Cursor.Pos.Row++
	clampColToLine(state)
	return true
}

// clampColToLine keeps the cursor column within the current line.
func clampColToLine(state *EditorState) {
	line := state.Buffer.Line(state.Cursor.Pos.Row)
	if state.Cursor.Pos.Col > len(line) {
		state.Cursor.Pos.Col = len(line)
	}
	state.Cursor.Pos.Col = clampByteIndexToRuneBoundary(line, state.Cursor.Pos.Col)
}
