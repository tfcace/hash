// internal/editor/cursor.go
package editor

// selectionKind is the selection state of a cursor.
type selectionKind int

const (
	selectNone selectionKind = iota // no selection; anchor is meaningless
	selectChar                      // charwise, from v or Shift+motion
	selectLine                      // linewise, from x: always whole lines
)

// Cursor tracks position and optional selection anchor.
type Cursor struct {
	Pos       Position // Current cursor position
	anchor    Position // Selection anchor; only meaningful when selection != selectNone
	selection selectionKind
}

// NewCursor creates a cursor at position 0,0.
func NewCursor() *Cursor {
	return &Cursor{Pos: Position{0, 0}}
}

// MoveTo moves the cursor to a new position.
func (c *Cursor) MoveTo(row, col int) {
	c.Pos.Row = row
	c.Pos.Col = col
}

// HasSelection returns true if there's an active selection.
func (c *Cursor) HasSelection() bool {
	return c.selection != selectNone
}

// StartSelection begins a charwise selection at the current position.
func (c *Cursor) StartSelection() {
	c.anchor = c.Pos
	c.selection = selectChar
}

// selectLines makes the selection linewise, covering whole lines from the
// anchor's row through the cursor's row (just the cursor's row when nothing
// was selected). The cursor ends at the far end of the span.
func (c *Cursor) selectLines(buf *Buffer) {
	anchorRow := c.Pos.Row
	if c.HasSelection() {
		anchorRow = c.anchor.Row
	}
	top, bottom := min(anchorRow, c.Pos.Row), max(anchorRow, c.Pos.Row)
	c.anchor = Position{Row: top, Col: 0}
	c.Pos = Position{Row: bottom, Col: len(buf.Line(bottom))}
	c.selection = selectLine
}

// ClearSelection removes the selection.
func (c *Cursor) ClearSelection() {
	c.selection = selectNone
	c.anchor = Position{}
}

// SelectionRange returns start and end positions (normalized so start < end).
// A linewise selection always spans whole lines of buf, wherever the cursor
// column has moved since.
func (c *Cursor) SelectionRange(buf *Buffer) (start, end Position) {
	if !c.HasSelection() {
		return c.Pos, c.Pos
	}

	a, b := c.anchor, c.Pos
	if a.Row > b.Row || (a.Row == b.Row && a.Col > b.Col) {
		a, b = b, a
	}
	if c.selection == selectLine {
		a.Col = 0
		b.Col = len(buf.Line(b.Row))
	}
	return a, b
}

// Clamp ensures the cursor, and the selection anchor, are within buffer bounds.
func (c *Cursor) Clamp(buf *Buffer) {
	c.Pos = clampPosition(buf, c.Pos)
	if c.HasSelection() {
		c.anchor = clampPosition(buf, c.anchor)
	}
}

func clampPosition(buf *Buffer, p Position) Position {
	p.Row = max(0, min(p.Row, buf.LineCount()-1))
	line := buf.Line(p.Row)
	p.Col = max(0, min(p.Col, len(line)))
	p.Col = clampByteIndexToRuneBoundary(line, p.Col)
	return p
}

// Clone creates a deep copy of the cursor.
func (c *Cursor) Clone() *Cursor {
	clone := *c
	return &clone
}
