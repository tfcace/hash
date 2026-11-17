// Package table draws rows of cells as a table that fits the pane. Replies
// and hash's own listings share it, so a table reads the same wherever it
// comes from.
package table

import (
	"regexp"
	"strings"

	"github.com/mattn/go-runewidth"
)

const (
	reset = "\033[0m"
	bold  = "\033[1m"
	gray  = "\033[90m"

	// gap separates two columns.
	gap = "  "
	// minColumn is the narrowest a column is squeezed to. A table that
	// does not fit the row even so is drawn as records, one cell a line.
	minColumn = 8
)

// Align is how a column's cells sit in it.
type Align int

const (
	Left Align = iota
	Right
	Center
)

// Render draws rows as aligned columns: the first row a bold header, a rule
// under it, two spaces between columns. Cells may carry SGR styling. Columns
// past the end of aligns are left-aligned. A table wider than width has its
// widest columns narrowed and their cells wrapped; one that cannot fit even
// so is drawn as records, each cell on a line after its column's name. A
// width below 1 is no limit. There is no newline after the last row.
func Render(rows [][]string, aligns []Align, width int) string {
	columns := 0
	for _, row := range rows {
		columns = max(columns, len(row))
	}
	if columns == 0 {
		return ""
	}
	aligns = append(append([]Align(nil), aligns...), make([]Align, max(columns-len(aligns), 0))...)

	widths := make([]int, columns)
	for i := range widths {
		widths[i] = 1
	}
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = max(widths[i], visibleWidth(cell))
		}
	}
	if !fitColumns(widths, width) {
		return records(rows)
	}

	var out []string
	for n, row := range rows {
		out = append(out, drawRow(row, widths, aligns, n == 0)...)
		if n == 0 {
			rules := make([]string, columns)
			for i, w := range widths {
				rules[i] = strings.Repeat("─", w)
			}
			out = append(out, gray+strings.Join(rules, gap)+reset)
		}
	}
	return strings.Join(out, "\n")
}

// token is one unit of a cell: a printable rune with its width, or an
// escape sequence with none.
type token struct {
	text  string
	width int
}

var sgrRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// escapeDone reports whether seq is a complete escape sequence: a CSI ends
// at its final byte, any other escape after one character.
func escapeDone(seq string) bool {
	if len(seq) < 2 {
		return false
	}
	if seq[1] != '[' {
		return true
	}
	last := seq[len(seq)-1]
	return len(seq) > 2 && last >= 0x40 && last <= 0x7e
}

// fitColumns narrows the widest columns until the table fits width, and
// reports whether it does. A width below 1 is no limit.
func fitColumns(widths []int, width int) bool {
	if width < 1 {
		return true
	}
	total := len(gap) * (len(widths) - 1)
	for _, w := range widths {
		total += w
	}
	for total > width {
		widest := 0
		for i, w := range widths {
			if w > widths[widest] {
				widest = i
			}
		}
		if widths[widest] <= minColumn {
			return false
		}
		widths[widest]--
		total--
	}
	return true
}

// drawRow draws one table row as screen rows: a cell too wide for its
// column continues on the rows below.
func drawRow(cells []string, widths []int, aligns []Align, header bool) []string {
	wrapped := make([][]string, len(widths))
	height := 1
	for i := range widths {
		if i < len(cells) {
			wrapped[i] = wrapCell(cells[i], widths[i])
		}
		height = max(height, len(wrapped[i]))
	}

	rows := make([]string, height)
	for n := range rows {
		var row strings.Builder
		for i, w := range widths {
			if i > 0 {
				row.WriteString(gap)
			}
			text := ""
			if n < len(wrapped[i]) {
				text = wrapped[i][n]
			}
			pad := w - visibleWidth(text)
			if header && text != "" {
				text = bold + strings.ReplaceAll(text, reset, reset+bold) + reset
			}
			before := 0
			switch aligns[i] {
			case Right:
				before = pad
			case Center:
				before = pad / 2
			}
			row.WriteString(strings.Repeat(" ", before) + text + strings.Repeat(" ", pad-before))
		}
		rows[n] = strings.TrimRight(row.String(), " ")
	}
	return rows
}

// records draws a table too wide for the row as one record a row:
// each cell on its own line, after its column's name.
func records(rows [][]string) string {
	header := rows[0]
	var records []string
	for _, row := range rows[1:] {
		var lines []string
		for i, cell := range row {
			if cell == "" {
				continue
			}
			if i < len(header) && header[i] != "" {
				cell = bold + strings.ReplaceAll(header[i], reset, reset+bold) + reset + ": " + cell
			}
			lines = append(lines, cell)
		}
		if len(lines) > 0 {
			records = append(records, strings.Join(lines, "\n"))
		}
	}
	if len(records) == 0 {
		return bold + strings.ReplaceAll(strings.Join(header, gap), reset, reset+bold) + reset
	}
	return strings.Join(records, "\n\n")
}

// wrapCell breaks a rendered cell into rows of at most width columns: at
// spaces where it can, and a word longer than the column (a path) after a
// separator in it, or anywhere when it has none in reach. Styling open at
// a break is closed there and reopened on the next row.
func wrapCell(cell string, width int) []string {
	var c cellRows
	spaces := 0
	for _, word := range cellWords(cell) {
		if word == nil {
			spaces++
			continue
		}
		wordWidth := 0
		for _, tok := range word {
			wordWidth += tok.width
		}
		switch {
		case c.col == 0:
		case c.col+spaces+wordWidth > width:
			c.breakAt(len(c.row))
		default:
			c.put(token{text: strings.Repeat(" ", spaces), width: spaces})
		}
		spaces = 0
		start := len(c.row) // where the word starts on this row
		for _, tok := range word {
			if c.col > 0 && c.col+tok.width > width {
				c.breakAt(c.wordBreak(start))
				start = 0
			}
			c.put(tok)
		}
	}
	c.breakAt(len(c.row))
	return c.rows
}

// cellRows is a cell being broken into rows.
type cellRows struct {
	rows []string
	row  []token // the row being filled
	col  int     // its columns
	sgr  string  // the styling open at its start
}

func (c *cellRows) put(tok token) {
	c.row = append(c.row, tok)
	c.col += tok.width
}

// wordBreak is where to break the word that fills the row from start:
// after its last separator, or at the row's end when it has none.
func (c *cellRows) wordBreak(start int) int {
	for i := len(c.row) - 1; i > start; i-- {
		if c.row[i].width > 0 && strings.ContainsAny(c.row[i].text, "/-_.,:=") {
			return i + 1
		}
	}
	return len(c.row)
}

// breakAt ends the row before its token at, which starts the next row.
func (c *cellRows) breakAt(at int) {
	var text strings.Builder
	text.WriteString(c.sgr)
	for _, tok := range c.row[:at] {
		text.WriteString(tok.text)
		switch {
		case tok.width > 0:
		case tok.text == reset:
			c.sgr = ""
		default:
			c.sgr += tok.text
		}
	}
	if c.sgr != "" {
		text.WriteString(reset)
	}
	c.rows = append(c.rows, text.String())
	c.row = append([]token(nil), c.row[at:]...)
	c.col = 0
	for _, tok := range c.row {
		c.col += tok.width
	}
}

// cellWords splits a rendered cell into its words, each a run of runes and
// escape sequences, with a nil entry for every space between them.
func cellWords(cell string) [][]token {
	var words [][]token
	var word []token
	var esc strings.Builder
	end := func() {
		if word != nil {
			words = append(words, word)
			word = nil
		}
	}
	for _, c := range cell {
		switch {
		case esc.Len() > 0:
			esc.WriteRune(c)
			if escapeDone(esc.String()) {
				word = append(word, token{text: esc.String()})
				esc.Reset()
			}
		case c == '\x1b':
			esc.WriteRune(c)
		case c == ' ':
			end()
			words = append(words, nil)
		default:
			word = append(word, token{text: string(c), width: runewidth.RuneWidth(c)})
		}
	}
	end()
	return words
}

// visibleWidth is the number of columns rendered text occupies, its escape
// sequences aside.
func visibleWidth(s string) int {
	return runewidth.StringWidth(sgrRe.ReplaceAllString(s, ""))
}
