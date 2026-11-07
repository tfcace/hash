package editor

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/termtext"
)

// vtScreen is the little of a terminal the display's frames use: printable
// text with autowrap (a wide character that does not fit wraps whole), CR,
// LF (scrolling at the bottom), cursor up/down/forward, erase in line and
// erase below. Other sequences (colors, cursor visibility) are skipped.
type vtScreen struct {
	width, height int
	cells         [][]string
	row, col      int
	pendingWrap   bool
	scrolled      int // rows pushed off the top into scrollback
}

func newVTScreen(width, height int) *vtScreen {
	s := &vtScreen{width: width, height: height, cells: make([][]string, height)}
	for i := range s.cells {
		s.cells[i] = make([]string, width)
	}
	return s
}

func (s *vtScreen) Write(p []byte) (int, error) {
	in := string(p)
	for i := 0; i < len(in); {
		switch c := in[i]; {
		case c == 0x1b:
			i += s.escape(in[i:])
		case c == '\r':
			s.col, s.pendingWrap = 0, false
			i++
		case c == '\n':
			s.lineFeed()
			i++
		case c < 0x20:
			i++
		default:
			end := termtext.ClusterEnd(in, i)
			s.put(in[i:end])
			i = end
		}
	}
	return len(p), nil
}

func (s *vtScreen) lineFeed() {
	s.pendingWrap = false
	if s.row < s.height-1 {
		s.row++
		return
	}
	s.cells = append(s.cells[1:], make([]string, s.width))
	s.scrolled++
}

func (s *vtScreen) put(c string) {
	w := termtext.Width(c)
	if w == 0 {
		return
	}
	if s.pendingWrap || s.col+w > s.width {
		s.col = 0
		s.lineFeed()
	}
	s.cells[s.row][s.col] = c
	for k := 1; k < w; k++ {
		s.cells[s.row][s.col+k] = ""
	}
	s.col += w
	if s.col >= s.width {
		s.col, s.pendingWrap = s.width-1, true
	}
}

// escape applies the sequence in starts with and returns its length.
func (s *vtScreen) escape(in string) int {
	if len(in) < 2 || in[1] != '[' {
		return min(2, len(in))
	}
	end := 2
	for end < len(in) && (in[end] < 0x40 || in[end] > 0x7e) {
		end++
	}
	if end == len(in) {
		return end
	}
	params, final := in[2:end], in[end]
	n, err := strconv.Atoi(params)
	if err != nil || n == 0 {
		n = 1
	}
	switch final {
	case 'A':
		s.row, s.pendingWrap = max(s.row-n, 0), false
	case 'B':
		s.row, s.pendingWrap = min(s.row+n, s.height-1), false
	case 'C':
		s.col, s.pendingWrap = min(s.col+n, s.width-1), false
	case 'K':
		if params == "2" {
			s.cells[s.row] = make([]string, s.width)
		} else {
			for k := s.col; k < s.width; k++ {
				s.cells[s.row][k] = ""
			}
		}
	case 'J':
		for k := s.col; k < s.width; k++ {
			s.cells[s.row][k] = ""
		}
		for r := s.row + 1; r < s.height; r++ {
			s.cells[r] = make([]string, s.width)
		}
	}
	return end + 1
}

// line is screen row r's text, its trailing blanks dropped.
func (s *vtScreen) line(r int) string {
	var b strings.Builder
	for _, c := range s.cells[r] {
		if c == "" {
			c = " "
		}
		b.WriteString(c)
	}
	return strings.TrimRight(b.String(), " ")
}

// tallBuffer is a buffer of n lines "line 00", "line 01", ...
func tallBuffer(n int) *Buffer {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d", i)
	}
	return NewBufferFromString(strings.Join(lines, "\n"))
}

// A buffer taller than the pane is drawn in a viewport the display owns:
// at most the pane's height of rows a frame, so no frame scrolls the
// screen, and the rows around the cursor, so the cursor stays on screen.
// It used to draw every row (the top ones scrolled into scrollback on
// every redraw) and move up by more rows than the screen has, leaving the
// cursor off its line.
func TestDisplay_TallBufferDrawsInAViewport(t *testing.T) {
	const width, height = 40, 10
	screen := newVTScreen(width, height)
	var frames bytes.Buffer
	d := NewDisplay(&frames, width, height)
	buf := tallBuffer(16)
	cur := &Cursor{Pos: Position{0, 0}}

	render := func(row int) {
		t.Helper()
		frames.Reset()
		cur.Pos = Position{row, 0}
		d.Render(buf, cur, false)
		frame := frames.String()
		if n := strings.Count(frame, "\n"); n > height-1 {
			t.Errorf("cursor on line %d: the frame writes %d rows, the pane has %d", row, n+1, height)
		}
		if _, err := screen.Write(frames.Bytes()); err != nil {
			t.Fatal(err)
		}
		if screen.scrolled != 0 {
			t.Fatalf("cursor on line %d: the frame scrolled %d rows into scrollback", row, screen.scrolled)
		}
		want := fmt.Sprintf("line %02d", row)
		if got := screen.line(screen.row); !strings.HasSuffix(got, want) {
			t.Fatalf("cursor on line %d: the cursor's screen row shows %q, want its line %q", row, got, want)
		}
		if screen.col != d.calcPrefixWidth(row) {
			t.Errorf("cursor on line %d: cursor at column %d, want %d", row, screen.col, d.calcPrefixWidth(row))
		}
	}
	render(0)
	if screen.row != 0 {
		t.Errorf("cursor on line 0 is on screen row %d, want 0", screen.row)
	}
	render(0) // a redraw draws the same rows in the same place
	for _, row := range []int{5, 9, 10, 15, 14, 7, 0} {
		render(row)
	}
	for r := 0; r < height; r++ {
		if want := fmt.Sprintf("line %02d", r); !strings.HasSuffix(screen.line(r), want) {
			t.Errorf("back at line 0, screen row %d shows %q, want %q", r, screen.line(r), want)
		}
	}
}

// One logical line wrapping over more rows than the pane has is cut by
// rows too: the rows around the cursor are drawn, none scroll.
func TestDisplay_TallWrappedLineDrawsInAViewport(t *testing.T) {
	const width, height = 20, 6
	screen := newVTScreen(width, height)
	var frames bytes.Buffer
	d := NewDisplay(&frames, width, height)
	var text strings.Builder
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&text, "%-20s", fmt.Sprintf("r%02d", i)) // a row of the wrapped line each, after the prefix
	}
	line := text.String()
	buf := NewBufferFromString(line)
	cur := &Cursor{Pos: Position{0, len(line)}}
	d.Render(buf, cur, false)
	if _, err := screen.Write(frames.Bytes()); err != nil {
		t.Fatal(err)
	}
	if screen.scrolled != 0 {
		t.Fatalf("the frame scrolled %d rows into scrollback", screen.scrolled)
	}
	_, row, col := d.layoutForStandardRender(buf, cur, "")
	if screen.row != height-1 || screen.col != min(col, width-1) {
		t.Errorf("cursor at screen %d/%d, want the last row, column %d (layout row %d)", screen.row, screen.col, col, row)
	}
	// The prefix takes a cell, so row k of the line starts with the last
	// space of chunk k-1: the row above the cursor's is " r11".
	if got := screen.line(height - 2); got != " r11" {
		t.Errorf("screen row %d is %q, want \" r11\"", height-2, got)
	}
}

// wrapRows cuts a drawn line where the terminal wraps it, a wide character
// that does not fit going whole to the next row, and starts each row with
// the colors in effect there, so a row drawn alone keeps them.
func TestWrapRows(t *testing.T) {
	got := wrapRows("ab\x1b[7mcd中e\x1b[0mfg", 5)
	want := []string{"ab\x1b[7mcd", "\x1b[7m中e\x1b[0mfg"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("wrapRows = %q, want %q", got, want)
	}
	got = wrapRows("abcdé", 5) // a mark joins the full row's last character
	if len(got) != 1 {
		t.Errorf("wrapRows = %q, want one row", got)
	}
}

// A menu or notice under an input taller than the pane, or one that only
// the rows under it make too tall, is drawn in the pane with the input:
// the viewport leaves the rows under the input free. They used to be
// written past the pane's bottom, scrolling the input into scrollback on
// every Tab and taking the cursor's row off the screen.
func TestEditor_RowsUnderATallInputStayInThePane(t *testing.T) {
	const width, height = 40, 10
	items := []Completion{{Text: "a"}, {Text: "b"}, {Text: "c"}, {Text: "d"}, {Text: "e"}, {Text: "f"}}
	for _, lines := range []int{16, 8} {
		for _, row := range []int{0, 2, 7, 9, 15} {
			if row >= lines {
				continue
			}
			screen := newVTScreen(width, height)
			var frames bytes.Buffer
			e := &Editor{display: NewDisplay(&frames, width, height), state: NewEditorState(), mode: NewInsertMode(), ghost: NewGhostText()}
			e.state.Buffer = tallBuffer(lines)
			e.state.Cursor.MoveTo(row, 7)
			draw := func(what string) {
				t.Helper()
				frames.Reset()
				e.render()
				if _, err := screen.Write(frames.Bytes()); err != nil {
					t.Fatal(err)
				}
				if screen.scrolled != 0 {
					t.Fatalf("%d lines, cursor on line %d, %s: scrolled %d rows into scrollback", lines, row, what, screen.scrolled)
				}
				want := fmt.Sprintf("line %02d", row)
				if got := screen.line(screen.row); !strings.HasSuffix(got, want) {
					t.Fatalf("%d lines, cursor on line %d, %s: the cursor's screen row shows %q, want %q", lines, row, what, got, want)
				}
			}
			e.menu = &completionMenu{items: items, from: 8*row + 5}
			draw("menu")
			e.menu.index = 1
			draw("menu again")
			e.menu = nil
			draw("menu closed")
			if lines > height {
				for r := 0; r < height; r++ {
					if strings.TrimSpace(screen.line(r)) == "" {
						t.Fatalf("%d lines, cursor on line %d: with the menu closed, screen row %d is blank", lines, row, r)
					}
				}
			}
			e.feedback = lineFeedback{kind: feedbackTimedOut}
			draw("notice")
		}
	}
}
