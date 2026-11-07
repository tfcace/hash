package termtext

import (
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// Plain is the text terminal output s shows, for a place that wants text
// and not terminal bytes (the clipboard): CRLF line ends become LF, escape
// sequences (SGR colors, OSC hyperlinks and titles, cursor moves) and other
// control characters are dropped, and a carriage return or backspace moves
// back over the line as it does on the terminal, so what is written after
// it replaces what it passes (a progress counter keeps its last value).
// Erase in line (CSI K) blanks the cells it erases, and a wide character
// takes two cells, as on the terminal. Tabs and line ends stay.
func Plain(s string) string {
	if isPlain(s) {
		return s
	}
	var out strings.Builder
	var line plainLine
	for i := 0; i < len(s); {
		if n := EscapeLen(s[i:]); n > 0 {
			line.escape(s[i : i+n])
			i += n
			continue
		}
		r, size := rune(s[i]), 1
		if r >= 0x80 {
			r, size = utf8.DecodeRuneInString(s[i:])
		}
		switch {
		case r == '\n':
			out.WriteString(line.String())
			out.WriteByte('\n')
			line = plainLine{cells: line.cells[:0]}
		case r == utf8.RuneError && size == 1:
			line.write("\ufffd", 1) // an invalid byte, as Inert draws it
		case r >= 0x20 && r != 0x7f && (r < 0x80 || r > 0x9f):
			end := printableEnd(s, i)
			line.text(s[i:end])
			size = end - i
		default:
			line.control(r)
		}
		i += size
	}
	out.WriteString(line.String())
	return out.String()
}

// printableEnd is where the run of printable text starting at i ends: at
// the next control character, invalid byte, or the end of s.
func printableEnd(s string, i int) int {
	for i < len(s) {
		r, size := rune(s[i]), 1
		if r >= 0x80 {
			r, size = utf8.DecodeRuneInString(s[i:])
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || (r == utf8.RuneError && size == 1) {
			return i
		}
		i += size
	}
	return i
}

// Cells of a plainLine besides written text: an erased (never written)
// cell, and the right half of a wide character.
const (
	erasedCell = ""
	wideTail   = "\x00"
)

// plainLine is the line Plain is writing, one entry a cell, and the column
// the cursor is at.
type plainLine struct {
	cells []string
	col   int
}

// text writes printable text at the cursor, a grapheme cluster a
// character.
func (l *plainLine) text(s string) {
	for i := 0; i < len(s); {
		next := ClusterEnd(s, i)
		l.write(s[i:next], runewidth.StringWidth(s[i:next]))
		i = next
	}
}

// control applies a control character: a carriage return or backspace
// moves the cursor, a tab is written; the others are dropped.
func (l *plainLine) control(r rune) {
	switch r {
	case '\r':
		l.col = 0
	case '\b':
		l.col = max(l.col-1, 0)
	case '\t':
		l.write("\t", 1)
	}
}

// write puts a character width cells wide at the cursor and moves past it.
// A zero-width character joins the one before it. Overwriting half of a
// wide character blanks its other half, as the terminal does.
func (l *plainLine) write(c string, width int) {
	if width == 0 {
		if prev := l.lead(l.col - 1); prev >= 0 {
			l.cells[prev] += c
		}
		return
	}
	for len(l.cells) < l.col+width {
		l.cells = append(l.cells, erasedCell)
	}
	l.unsplit(l.col)
	l.unsplit(l.col + width - 1)
	l.cells[l.col] = c
	for k := 1; k < width; k++ {
		l.cells[l.col+k] = wideTail
	}
	l.col += width
}

// lead is the cell holding the character that covers cell i, -1 when none.
func (l *plainLine) lead(i int) int {
	for i >= 0 && i < len(l.cells) && l.cells[i] == wideTail {
		i--
	}
	if i < 0 || i >= len(l.cells) {
		return -1
	}
	return i
}

// unsplit erases the whole wide character covering cell i, so writing over
// part of it leaves no half character behind, and the cells it covered are
// erased cells (a space inside the line, nothing at its end), as the
// terminal leaves them.
func (l *plainLine) unsplit(i int) {
	lead := l.lead(i)
	if lead < 0 || (lead == i && (i+1 >= len(l.cells) || l.cells[i+1] != wideTail)) {
		return
	}
	l.cells[lead] = erasedCell
	for k := lead + 1; k < len(l.cells) && l.cells[k] == wideTail; k++ {
		l.cells[k] = erasedCell
	}
}

// escape applies an escape sequence that changes the line's text: erase in
// line (CSI K: 0 to the end, 1 to the start, 2 the whole line). The cursor
// stays where it is.
func (l *plainLine) escape(seq string) {
	if len(seq) < 3 || seq[1] != '[' || seq[len(seq)-1] != 'K' {
		return
	}
	switch seq[2 : len(seq)-1] {
	case "", "0":
		if l.col < len(l.cells) {
			l.unsplit(l.col)
			l.cells = l.cells[:l.col]
		}
	case "1":
		end := min(l.col, len(l.cells)-1)
		if end >= 0 {
			l.unsplit(end)
			for k := 0; k <= end; k++ {
				l.cells[k] = erasedCell
			}
		}
	case "2":
		l.cells = l.cells[:0]
	}
}

// String is the line's text: an erased cell inside it is a space, and the
// erased cells it ends with are dropped.
func (l *plainLine) String() string {
	end := len(l.cells)
	for end > 0 && l.cells[end-1] == erasedCell {
		end--
	}
	var b strings.Builder
	for _, c := range l.cells[:end] {
		switch c {
		case wideTail:
		case erasedCell:
			b.WriteByte(' ')
		default:
			b.WriteString(c)
		}
	}
	return b.String()
}

// EscapeLen is the length of the escape sequence s starts with, 0 when it
// starts with none: CSI (ESC [ ... final byte), a string sequence (OSC,
// DCS, SOS, PM, APC: ESC ] P X ^ _ ... up to ST or, for OSC, BEL), or ESC
// with its intermediate bytes and final byte (a charset choice, ESC 7). A
// sequence the output ends inside runs to its end.
func EscapeLen(s string) int {
	if s == "" || s[0] != 0x1b {
		return 0
	}
	if len(s) == 1 {
		return 1
	}
	switch s[1] {
	case '[':
		return csiLen(s)
	case ']', 'P', 'X', '^', '_':
		return stringSequenceLen(s)
	}
	i := 1
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i < len(s) {
		i++ // the final byte
	}
	return i
}

// csiLen is the length of the CSI sequence s starts with (ESC [): up to
// its final byte.
func csiLen(s string) int {
	for i := 2; i < len(s); i++ {
		if s[i] >= 0x40 && s[i] <= 0x7e {
			return i + 1
		}
	}
	return len(s)
}

// stringSequenceLen is the length of the string sequence s starts with
// (ESC ] P X ^ _): up to ST, or BEL for OSC.
func stringSequenceLen(s string) int {
	for i := 2; i < len(s); i++ {
		switch {
		case s[i] == 0x07 && s[1] == ']':
			return i + 1
		case s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\':
			return i + 2
		}
	}
	return len(s)
}
