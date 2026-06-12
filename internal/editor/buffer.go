package editor

import (
	"strings"
	"unicode/utf8"
)

// Position represents a row/column position in the buffer.
type Position struct {
	Row, Col int
}

// Buffer holds the text content as a slice of lines.
type Buffer struct {
	lines []string
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

// Clone creates a deep copy of the buffer.
func (b *Buffer) Clone() *Buffer {
	lines := make([]string, len(b.lines))
	copy(lines, b.lines)
	return &Buffer{lines: lines}
}

// ReplaceLine replaces the content of a line (0-indexed).
func (b *Buffer) ReplaceLine(row int, content string) {
	if row < 0 || row >= len(b.lines) {
		return
	}
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

func previousRuneBoundary(s string, col int) int {
	col = clampByteIndexToRuneBoundary(s, col)
	if col <= 0 {
		return 0
	}
	_, size := utf8.DecodeLastRuneInString(s[:col])
	if size <= 0 {
		return col - 1
	}
	return col - size
}

func nextRuneBoundary(s string, col int) int {
	col = clampByteIndexToRuneBoundary(s, col)
	if col >= len(s) {
		return len(s)
	}
	_, size := utf8.DecodeRuneInString(s[col:])
	if size <= 0 {
		return col + 1
	}
	return col + size
}
