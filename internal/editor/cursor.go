// internal/editor/cursor.go
package editor

// Cursor tracks position and optional selection anchor.
type Cursor struct {
	Pos    Position  // Current cursor position
	Anchor *Position // Selection anchor (nil if no selection)
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
	return c.Anchor != nil
}

// StartSelection begins a selection at the current position.
func (c *Cursor) StartSelection() {
	anchor := c.Pos // Copy
	c.Anchor = &anchor
}

// ClearSelection removes the selection.
func (c *Cursor) ClearSelection() {
	c.Anchor = nil
}

// SelectionRange returns start and end positions (normalized so start < end).
func (c *Cursor) SelectionRange() (start, end Position) {
	if c.Anchor == nil {
		return c.Pos, c.Pos
	}

	a, b := *c.Anchor, c.Pos
	if a.Row > b.Row || (a.Row == b.Row && a.Col > b.Col) {
		a, b = b, a
	}
	return a, b
}

// Clamp ensures cursor is within buffer bounds.
func (c *Cursor) Clamp(buf *Buffer) {
	if c.Pos.Row < 0 {
		c.Pos.Row = 0
	}
	if c.Pos.Row >= buf.LineCount() {
		c.Pos.Row = buf.LineCount() - 1
	}

	lineLen := len(buf.Line(c.Pos.Row))
	if c.Pos.Col < 0 {
		c.Pos.Col = 0
	}
	if c.Pos.Col > lineLen {
		c.Pos.Col = lineLen
	}
}

// Clone creates a deep copy of the cursor.
func (c *Cursor) Clone() *Cursor {
	clone := &Cursor{Pos: c.Pos}
	if c.Anchor != nil {
		anchor := *c.Anchor
		clone.Anchor = &anchor
	}
	return clone
}
