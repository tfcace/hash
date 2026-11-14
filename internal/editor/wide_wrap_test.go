package editor

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/termtext"
)

// A wide character that does not fit in a row's last cell is drawn at the
// start of the next row, the cell it skips left empty, as terminals do. The
// layout used to count width cells to every row, so the cursor and the row
// count came out one cell short after it, and the next redraw moved to the
// wrong place.
func TestDisplay_WideCharacterAtTheEdgeWrapsWhole(t *testing.T) {
	d := NewDisplay(&bytes.Buffer{}, 10, 8)
	line := "abcdefgh中x" // prefix 1 + 8 = 9 cells: 中 cannot take the 10th
	rows, row, col := d.layoutForStandardRender(NewBufferFromString(line), &Cursor{Pos: Position{0, len(line)}}, "")
	if rows != 2 || row != 1 || col != 3 {
		t.Errorf("layout of %q = %d rows, cursor %d/%d; want 2 rows, cursor at row 1 cell 3 (中 at 0-1, x at 2)", line, rows, row, col)
	}
	line = "abcdefgh" + strings.Repeat("中", 5) // 中 ends exactly at the second row's last cell
	rows, row, col = d.layoutForStandardRender(NewBufferFromString(line), &Cursor{Pos: Position{0, len(line)}}, "")
	if rows != 2 || row != 1 || col != 10 {
		t.Errorf("layout of %q = %d rows, cursor %d/%d; want 2 rows, cursor at row 1 cell 10 (pending wrap)", line, rows, row, col)
	}
}

// Up from the end of a row that starts with a pushed wide character keeps
// the screen column, and the menu opens under a word after one.
func TestEditor_WideCharacterAtTheEdgeInMotionAndMenu(t *testing.T) {
	var out bytes.Buffer
	ed := New(Config{}, strings.NewReader(""), &out)
	ed.display.Resize(10, 8)
	line := "abcdefgh中x"
	ed.SetInitialText(line)
	ed.handleKeyEvent(Key{Special: KeyUp})
	if got := ed.state.Cursor.Pos.Col; got != 2 {
		t.Errorf("Up from cell 3 of the second row lands at byte %d, want 2 (cell 3 of the first)", got)
	}
	if got := ed.display.lineCells(0, line, len("abcdefgh中")); got != 11 {
		t.Errorf("x starts %d cells into its line, want 11 (the skipped cell counts)", got)
	}
}

// A tab is drawn as TabWidth spaces and a control character as two cells of
// caret notation: narrow cells the terminal wraps one at a time. Only a
// glyph drawn as itself is pushed whole to the next row; the layout used to
// push a tab or ^A at the edge too, leaving the cursor a cell to the right
// of where the terminal draws it.
func TestDisplay_TabOrControlAtTheEdgeWrapsByCell(t *testing.T) {
	d := NewDisplay(&bytes.Buffer{}, 10, 8)
	for _, tt := range []struct {
		line     string
		row, col int
	}{
		{"abcdefgh\tx", 1, 4},   // prefix 1 + 8 = 9; the tab's spaces at 9..12, x at 13
		{"abcdefgh\x01x", 1, 2}, // ^ at 9, A at 10 (row 1 cell 0), x at 11
	} {
		_, row, col := d.layoutForStandardRender(NewBufferFromString(tt.line), &Cursor{Pos: Position{0, len(tt.line)}}, "")
		if row != tt.row || col != tt.col {
			t.Errorf("cursor after %q at row %d cell %d; the terminal draws it at row %d cell %d", tt.line, row, col, tt.row, tt.col)
		}
	}
}

// A ghost after the line wraps as the terminal draws it: a wide character
// that does not fit in its row's last cell is pushed to the next row. The
// layout added the ghost's width unwrapped, counted a row fewer than the
// terminal drew, and the next redraw started a row low, stacking prompts.
func TestDisplay_GhostWideCharacterAtTheEdgeWrapsWhole(t *testing.T) {
	d := NewDisplay(&bytes.Buffer{}, 10, 8)
	line := "abcdefgh" // prefix 1 + 8 = 9 cells
	buf := NewBufferFromString(line)
	cur := &Cursor{Pos: Position{0, len(line)}}
	ghost := "中123456789" // 中 cannot take cell 9: pushed, the ghost ends on a third row
	d.RenderWithGhost(buf, cur, false, ghost, GhostRenderState{})
	if d.drawn.rows != 3 {
		t.Errorf("layout counts %d rows for the line and its ghost; the terminal draws 3", d.drawn.rows)
	}
}

// The ASCII fast paths count and step as grapheme segmentation does:
// a combining mark, a ZWJ or a CR LF after ASCII still joins its cluster.
func TestASCIIFastPathsMatchSegmentation(t *testing.T) {
	lines := []string{
		"plain ascii line", "e\u0301x", "ab\r\ncd", "a\u200db", "x中y", "\t\x01z", "\U0001F1FA\U0001F1F8ab", "ab\U0001F3F3\uFE0F\u200d\U0001F308c",
		strings.Repeat("ab中", 7) + "e\u0301",
	}
	for _, line := range lines {
		for upto := 0; upto <= len(line); upto++ {
			slow := 1
			for i := 0; i < upto; {
				i, slow = nextCell(10, slow, line, i)
			}
			if got := wrapCells(10, 1, line, upto); got != slow {
				t.Errorf("wrapCells(%q, %d) = %d, segmentation says %d", line, upto, got, slow)
			}
			naive, start := 0, 0
			col := clampByteIndexToRuneBoundary(line, upto)
			for start < col {
				end := termtext.ClusterEnd(line, start)
				if end >= col {
					naive = start
					break
				}
				start = end
			}
			if got := previousClusterBoundary(line, upto); got != naive {
				t.Errorf("previousClusterBoundary(%q, %d) = %d, segmentation says %d", line, upto, got, naive)
			}
		}
	}
}
