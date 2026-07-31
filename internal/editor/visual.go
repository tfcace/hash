package editor

import (
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// LineLayout exposes the display's wrap geometry to cursor motion so
// vertical movement can operate on visual (soft-wrapped) rows.
type LineLayout interface {
	// WrapWidth is the terminal width used for soft wrapping.
	WrapWidth() int
	// PrefixWidth is the visible width rendered before a logical line
	// (prompt on row 0, gutter/space on continuation rows).
	PrefixWidth(row int) int
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
	abs := layout.PrefixWidth(row) + visibleWidthAtByteIndex(line, col)
	vr := abs / width
	goal := state.currentGoal(abs % width)

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
	abs := layout.PrefixWidth(row) + visibleWidthAtByteIndex(line, col)
	vr := abs / width
	goal := state.currentGoal(abs % width)

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
	if s.goalCol >= 0 && s.goalAt == s.Cursor.Pos {
		return s.goalCol
	}
	return fallback
}

// rememberGoal stores the goal column against the cursor's new position.
func (s *EditorState) rememberGoal(goal int) {
	s.goalCol = goal
	s.goalAt = s.Cursor.Pos
}

// lastVisualRow returns the last visual row offset of the logical line.
func lastVisualRow(layout LineLayout, line string, row int) int {
	total := layout.PrefixWidth(row) + visibleWidth(line)
	if total <= 0 {
		return 0
	}
	return (total - 1) / layout.WrapWidth()
}

// byteColForAbs returns the byte column in line whose visual position is
// closest to, but not past, absolute screen column target. Wide runes are
// never split: a target inside one lands before it.
func byteColForAbs(layout LineLayout, line string, row, target int) int {
	abs := layout.PrefixWidth(row)
	col := 0
	for col < len(line) {
		r, size := utf8.DecodeRuneInString(line[col:])
		w := runewidth.RuneWidth(r)
		if abs+w > target {
			break
		}
		abs += w
		col += size
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
