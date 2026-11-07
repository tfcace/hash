package editor

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var rowStart = regexp.MustCompile(`^\x1b\[(\d+)C`)

// menuRowCells is how many cells each menu row drawn in out takes: the
// cursor-forward that starts it plus what it prints.
func menuRowCells(out string) []int {
	var cells []int
	for _, row := range strings.Split(out, "\r\n")[1:] {
		row = strings.TrimPrefix(row, ansiClearLine)
		col := 0
		if m := rowStart.FindStringSubmatch(row); m != nil {
			col, _ = strconv.Atoi(m[1])
			row = row[len(m[0]):]
		}
		if i := strings.Index(row, "\x1b["); i >= 0 && strings.Contains(row[i:], "A\r") {
			row = row[:strings.LastIndex(row, "\x1b[")] // the cursor-up back to the input
		}
		cells = append(cells, col+visibleWidth(row))
	}
	return cells
}

// A menu row wider than the terminal wraps onto a row the cursor-up back
// to the input does not count, and the next redraw stacks a prompt. The
// menu opens under the word, unless that leaves too little room: then it
// moves left. A word starting exactly at a wrapped row's first cell used
// to open the menu at the right edge (the cell count's row end, not 0), and
// one starting near the edge squeezed every row to one cell of text that
// still overflowed.
func TestDisplay_CompletionMenuStaysInsideTheTerminal(t *testing.T) {
	items := []CompletionItem{{Text: "foo"}, {Text: "foobar", Description: "a longer description"}}
	for _, tc := range []struct {
		name string
		line string
		word int // byte where the completed word starts
	}{
		{"near the right edge", strings.Repeat("a", 15) + " fo", 16},
		{"at a wrapped row's start", strings.Repeat("a", 18) + " fo", 19},
		{"on the second wrapped row", strings.Repeat("a", 30) + " fo", 31},
	} {
		var out bytes.Buffer
		d := NewDisplay(&out, 20, 10)
		d.SetScrollbarColor("#ff0000")
		d.Render(NewBufferFromString(tc.line), &Cursor{Pos: Position{0, len(tc.line)}}, false)
		out.Reset()
		d.RenderCompletionMenu(items, 0, 0, visibleWidthAtByteIndex(tc.line, tc.word), "")
		cells := menuRowCells(out.String())
		if len(cells) != len(items) {
			t.Fatalf("%s: %d menu rows in %q, want %d", tc.name, len(cells), out.String(), len(items))
		}
		for i, c := range cells {
			if c > 20 {
				t.Errorf("%s: menu row %d takes %d cells on a 20-cell terminal: %q", tc.name, i, c, out.String())
			}
		}
		if !strings.Contains(out.String(), "foobar") {
			t.Errorf("%s: the menu cut its items: %q", tc.name, out.String())
		}
	}
}
