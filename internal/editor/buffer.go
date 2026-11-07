package editor

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/tfcace/hash/internal/termtext"
)

// Position represents a row/column position in the buffer.
type Position struct {
	Row, Col int
}

// Buffer holds the text content as a slice of lines. A clone shares the
// lines until either side writes (copy on write), so an undo snapshot of an
// unchanged buffer costs nothing.
type Buffer struct {
	lines  []string
	shared bool // lines may be shared with a clone: copy before writing in place
}

// NewBuffer creates an empty buffer with one empty line.
func NewBuffer() *Buffer {
	return &Buffer{lines: []string{""}}
}

// NewBufferFromString creates a buffer from a string.
func NewBufferFromString(s string) *Buffer {
	if s == "" {
		return NewBuffer()
	}
	return &Buffer{lines: strings.Split(s, "\n")}
}

// LineCount returns the number of lines.
func (b *Buffer) LineCount() int {
	return len(b.lines)
}

// Line returns the content of a line (0-indexed).
func (b *Buffer) Line(row int) string {
	if row < 0 || row >= len(b.lines) {
		return ""
	}
	return b.lines[row]
}

// Content returns the full buffer content with newlines.
func (b *Buffer) Content() string {
	return strings.Join(b.lines, "\n")
}

// Insert inserts text at the given position.
func (b *Buffer) Insert(row, col int, text string) {
	if row < 0 || row >= len(b.lines) {
		return
	}

	line := b.lines[row]
	col = clampByteIndexToRuneBoundary(line, col)

	// Handle newlines in inserted text
	parts := strings.Split(text, "\n")
	if len(parts) == 1 {
		b.own()
		b.lines[row] = line[:col] + text + line[col:]
		return
	}

	// Multi-line insert
	before := line[:col]
	after := line[col:]

	newLines := make([]string, 0, len(b.lines)+len(parts)-1)
	newLines = append(newLines, b.lines[:row]...)
	newLines = append(newLines, before+parts[0])
	for i := 1; i < len(parts)-1; i++ {
		newLines = append(newLines, parts[i])
	}
	newLines = append(newLines, parts[len(parts)-1]+after)
	newLines = append(newLines, b.lines[row+1:]...)
	b.lines = newLines
}

// Delete removes text between two positions.
func (b *Buffer) Delete(from, to Position) {
	// Ensure from comes before to
	if from.Row > to.Row || (from.Row == to.Row && from.Col > to.Col) {
		from, to = to, from
	}

	if from.Row == to.Row {
		line := b.lines[from.Row]
		from.Col = clampByteIndexToRuneBoundary(line, from.Col)
		to.Col = clampByteIndexToRuneBoundary(line, to.Col)
		b.own()
		b.lines[from.Row] = line[:from.Col] + line[to.Col:]
		return
	}

	// Multi-line delete
	start := b.lines[from.Row]
	end := b.lines[to.Row]
	from.Col = clampByteIndexToRuneBoundary(start, from.Col)
	to.Col = clampByteIndexToRuneBoundary(end, to.Col)
	startLine := start[:from.Col]
	endLine := end[to.Col:]

	newLines := make([]string, 0, len(b.lines)-(to.Row-from.Row))
	newLines = append(newLines, b.lines[:from.Row]...)
	newLines = append(newLines, startLine+endLine)
	newLines = append(newLines, b.lines[to.Row+1:]...)
	b.lines = newLines
}

// Clone returns a copy of the buffer that later writes to either do not
// reach. The lines are shared until then.
func (b *Buffer) Clone() *Buffer {
	b.shared = true
	return &Buffer{lines: b.lines, shared: true}
}

// own gives the buffer lines of its own before a write in place. (Writes
// that build new lines leave the shared ones alone.)
func (b *Buffer) own() {
	if b.shared {
		b.lines = slices.Clone(b.lines)
		b.shared = false
	}
}

// sameText reports whether b and o hold the same text, without joining
// either: lines shared with a clone compare by pointer.
func (b *Buffer) sameText(o *Buffer) bool {
	return slices.Equal(b.lines, o.lines)
}

// size is the bytes of text the buffer holds.
func (b *Buffer) size() int {
	n := len(b.lines) // the newlines
	for _, l := range b.lines {
		n += len(l)
	}
	return n
}

// ReplaceLine replaces the content of a line (0-indexed).
func (b *Buffer) ReplaceLine(row int, content string) {
	if row < 0 || row >= len(b.lines) {
		return
	}
	b.own()
	b.lines[row] = content
}

func clampByteIndexToRuneBoundary(s string, col int) int {
	if col <= 0 {
		return 0
	}
	if col >= len(s) {
		return len(s)
	}
	for col > 0 && !utf8.RuneStart(s[col]) {
		col--
	}
	return col
}

// previousClusterBoundary is where the grapheme cluster (a character with
// its combining marks, a ZWJ emoji, a flag) before byte col starts: Left
// and Backspace step over whole clusters, as widths count them.
func previousClusterBoundary(s string, col int) int {
	col = clampByteIndexToRuneBoundary(s, col)
	start := clusterBoundaryBefore(s, col)
	for start < col {
		end := termtext.ClusterEnd(s, start)
		if end >= col {
			return start
		}
		start = end
	}
	return 0
}

// clusterBoundaryBefore is a grapheme cluster boundary before byte col, as
// near it as a cheap look back finds: between two ASCII bytes (other than
// CR LF) clusters always break, since no ASCII character extends or joins
// another. Else 0, the line's start.
func clusterBoundaryBefore(s string, col int) int {
	for p := col - 1; p > 0; p-- {
		if s[p-1] < 0x80 && s[p] < 0x80 && (s[p-1] != '\r' || s[p] != '\n') {
			return p
		}
	}
	return 0
}

// nextClusterBoundary is where the grapheme cluster at byte col ends.
func nextClusterBoundary(s string, col int) int {
	col = clampByteIndexToRuneBoundary(s, col)
	if col >= len(s) {
		return len(s)
	}
	return termtext.ClusterEnd(s, col)
}
