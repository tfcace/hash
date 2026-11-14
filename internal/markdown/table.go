package markdown

import (
	"regexp"
	"strings"

	"github.com/mattn/go-runewidth"
)

// A table in a reply is drawn as aligned columns: a bold header, a rule
// under it, and two spaces between columns. Its lines are held until the
// table ends, because a column's width is known only then.
const (
	tableGap = "  "
	// minColumn is the narrowest a column is squeezed to. A table that
	// does not fit the row even so is drawn as records, one cell a line.
	minColumn = 8
)

type cellAlign int

const (
	alignLeft cellAlign = iota
	alignRight
	alignCenter
)

// isTableLine reports whether a line can belong to a table: its text
// starts with a pipe.
func isTableLine(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), "|")
}

var ruleCellRe = regexp.MustCompile(`^:?-+:?$`)

// isTableRule reports whether a line is the row under a table's header
// ("|---|:--:|"), the row that makes the lines around it a table.
func isTableRule(line string) bool {
	cells := splitRow(line)
	for _, c := range cells {
		if !ruleCellRe.MatchString(c) {
			return false
		}
	}
	return len(cells) > 0
}

// splitRow is the cells of a table line. A pipe after a backslash is the
// cell's own.
func splitRow(line string) []string {
	rs := []rune(strings.TrimPrefix(strings.TrimSpace(line), "|"))
	var cells []string
	var cell strings.Builder
	for i := 0; i < len(rs); i++ {
		switch {
		case rs[i] == '\\' && i+1 < len(rs) && rs[i+1] == '|':
			cell.WriteRune('|')
			i++
		case rs[i] == '|':
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		case rs[i] == '\t':
			cell.WriteRune(' ')
		default:
			cell.WriteRune(rs[i])
		}
	}
	if rest := strings.TrimSpace(cell.String()); rest != "" {
		cells = append(cells, rest)
	}
	return cells
}

// holdTableLine keeps a line that may belong to a table. A first line not
// followed by the rule row was no header: it is drawn as text and the new
// line takes its place.
func (r *StreamingRenderer) holdTableLine(content string, hasNewline bool) string {
	var out string
	if len(r.table) == 1 && !isTableRule(content) {
		out = r.endTable()
	}
	r.table = append(r.table, content)
	r.tableOpen = !hasNewline
	return out
}

// endTable draws the lines held for a table, if any.
func (r *StreamingRenderer) endTable() string {
	if len(r.table) == 0 {
		return ""
	}
	lines, open := r.table, r.tableOpen
	r.table, r.tableOpen = nil, false

	var out string
	if len(lines) >= 2 {
		out = r.renderTable(lines)
	} else {
		out = r.processInline(lines[0], true)
	}
	if !open {
		out += "\n"
	}
	return out
}

// renderTable draws a header line, its rule row and the rows after it,
// without a newline after the last row.
func (r *StreamingRenderer) renderTable(lines []string) string {
	rows := [][]string{r.styledRow(lines[0])}
	for _, line := range lines[2:] {
		rows = append(rows, r.styledRow(line))
	}
	columns := 0
	for _, row := range rows {
		columns = max(columns, len(row))
	}
	if columns == 0 {
		return ""
	}

	aligns := columnAligns(lines[1], columns)
	widths := make([]int, columns)
	for i := range widths {
		widths[i] = 1
	}
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = max(widths[i], visibleWidth(cell))
		}
	}
	if !fitColumns(widths, r.Width) {
		return tableAsRecords(rows)
	}

	var out []string
	for n, row := range rows {
		out = append(out, tableRow(row, widths, aligns, n == 0)...)
		if n == 0 {
			rules := make([]string, columns)
			for i, w := range widths {
				rules[i] = strings.Repeat("─", w)
			}
			out = append(out, gray+strings.Join(rules, tableGap)+reset)
		}
	}
	return strings.Join(out, "\n")
}

// columnAligns is how each column is aligned, as the rule row says with
// its colons.
func columnAligns(rule string, columns int) []cellAlign {
	aligns := make([]cellAlign, columns)
	for i, c := range splitRow(rule) {
		if i >= columns {
			break
		}
		left, right := strings.HasPrefix(c, ":"), strings.HasSuffix(c, ":")
		switch {
		case left && right:
			aligns[i] = alignCenter
		case right:
			aligns[i] = alignRight
		}
	}
	return aligns
}

// styledRow is a table line's cells with their inline markup rendered.
func (r *StreamingRenderer) styledRow(line string) []string {
	cells := splitRow(line)
	for i, c := range cells {
		cells[i] = r.processInline(c, true)
	}
	return cells
}

// fitColumns narrows the widest columns until the table fits width, and
// reports whether it does. A width below 1 is no limit.
func fitColumns(widths []int, width int) bool {
	if width < 1 {
		return true
	}
	total := len(tableGap) * (len(widths) - 1)
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

// tableRow draws one table row as screen rows: a cell too wide for its
// column continues on the rows below.
func tableRow(cells []string, widths []int, aligns []cellAlign, header bool) []string {
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
				row.WriteString(tableGap)
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
			case alignRight:
				before = pad
			case alignCenter:
				before = pad / 2
			}
			row.WriteString(strings.Repeat(" ", before) + text + strings.Repeat(" ", pad-before))
		}
		rows[n] = strings.TrimRight(row.String(), " ")
	}
	return rows
}

// tableAsRecords draws a table too wide for the row as one record a row:
// each cell on its own line, after its column's name.
func tableAsRecords(rows [][]string) string {
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
		return bold + strings.ReplaceAll(strings.Join(header, tableGap), reset, reset+bold) + reset
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
			c.put(wrapToken{text: strings.Repeat(" ", spaces), width: spaces})
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
	row  []wrapToken // the row being filled
	col  int         // its columns
	sgr  string      // the styling open at its start
}

func (c *cellRows) put(tok wrapToken) {
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
	c.row = append([]wrapToken(nil), c.row[at:]...)
	c.col = 0
	for _, tok := range c.row {
		c.col += tok.width
	}
}

// cellWords splits a rendered cell into its words, each a run of runes and
// escape sequences, with a nil entry for every space between them.
func cellWords(cell string) [][]wrapToken {
	var words [][]wrapToken
	var word []wrapToken
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
				word = append(word, wrapToken{text: esc.String()})
				esc.Reset()
			}
		case c == '\x1b':
			esc.WriteRune(c)
		case c == ' ':
			end()
			words = append(words, nil)
		default:
			word = append(word, wrapToken{text: string(c), width: runewidth.RuneWidth(c)})
		}
	}
	end()
	return words
}

// visibleWidth is the number of columns rendered text occupies, its escape
// sequences aside.
func visibleWidth(s string) int {
	return displayWidth(sgrRe.ReplaceAllString(s, ""))
}
