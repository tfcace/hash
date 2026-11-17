package markdown

import (
	"regexp"
	"strings"

	"github.com/tfcace/hash/internal/table"
)

// A table in a reply is drawn by the renderer hash's own tables share
// (internal/table). Its lines are held until the table ends, because a
// column's width is known only then.

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
	return table.Render(rows, columnAligns(lines[1]), r.Width)
}

// columnAligns is how each column is aligned, as the rule row says with
// its colons.
func columnAligns(rule string) []table.Align {
	cells := splitRow(rule)
	aligns := make([]table.Align, len(cells))
	for i, c := range cells {
		left, right := strings.HasPrefix(c, ":"), strings.HasSuffix(c, ":")
		switch {
		case left && right:
			aligns[i] = table.Center
		case right:
			aligns[i] = table.Right
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
