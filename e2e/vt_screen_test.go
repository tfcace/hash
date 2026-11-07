//go:build e2e

package e2e

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tfcace/hash/internal/termtext"
)

// vtScreen is the little of a terminal hash's output needs: printable text
// with autowrap, CR, LF and index (scrolling at the bottom, counted in
// scrolled), cursor movement (CUU/CUD/CUF/CUB/CNL/CPL/CHA/CUP/VPA), save and
// restore, erase in line and display, scroll up/down. It answers the
// queries hash may send (cursor position, device status and attributes,
// colors, DECRQM) as a terminal does. Other sequences are skipped.
type vtScreen struct {
	width, height int
	cells         [][]string
	row, col      int
	savedRow      int
	savedCol      int
	pendingWrap   bool
	scrolled      int    // rows pushed off the top into scrollback
	partial       []byte // an escape sequence a write ended in the middle of
}

func newVTScreen(width, height int) *vtScreen {
	s := &vtScreen{width: width, height: height, cells: make([][]string, height)}
	for i := range s.cells {
		s.cells[i] = make([]string, width)
	}
	return s
}

func (s *vtScreen) Write(p []byte) (int, error) {
	s.feed(p)
	return len(p), nil
}

// feed applies p and returns the terminal's answers to the queries in it.
func (s *vtScreen) feed(p []byte) []byte {
	in := string(append(s.partial, p...))
	s.partial = nil
	var replies []byte
	for i := 0; i < len(in); {
		switch c := in[i]; {
		case c == 0x1b:
			n, reply := s.escape(in[i:])
			if n == 0 { // incomplete: wait for the rest
				s.partial = []byte(in[i:])
				return replies
			}
			replies = append(replies, reply...)
			i += n
		case c == '\r':
			s.col, s.pendingWrap = 0, false
			i++
		case c == '\n':
			s.lineFeed()
			i++
		case c == '\b':
			s.col, s.pendingWrap = max(s.col-1, 0), false
			i++
		case c < 0x20:
			i++
		default:
			end := termtext.ClusterEnd(in, i)
			if end <= i {
				end = i + 1
			}
			s.put(in[i:end])
			i = end
		}
	}
	return replies
}

func (s *vtScreen) blankRow() []string { return make([]string, s.width) }

func (s *vtScreen) lineFeed() {
	s.pendingWrap = false
	if s.row < s.height-1 {
		s.row++
		return
	}
	s.scrollUp(1)
}

func (s *vtScreen) scrollUp(n int) {
	for range min(n, s.height) {
		s.cells = append(s.cells[1:], s.blankRow())
		s.scrolled++
	}
}

func (s *vtScreen) scrollDown(n int) {
	for range min(n, s.height) {
		s.cells = append([][]string{s.blankRow()}, s.cells[:s.height-1]...)
	}
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
	for k := 1; k < w && s.col+k < s.width; k++ {
		s.cells[s.row][s.col+k] = ""
	}
	s.col += w
	if s.col >= s.width {
		s.col, s.pendingWrap = s.width-1, true
	}
}

// escape applies the sequence in starts with and returns its length (0 when
// in holds only part of it) and the terminal's reply to it, if any.
func (s *vtScreen) escape(in string) (int, []byte) {
	if len(in) < 2 {
		return 0, nil
	}
	switch in[1] {
	case '[':
		return s.csi(in)
	case ']', 'P', '_', '^': // OSC, DCS, APC, PM: up to BEL or ST
		for i := 2; i < len(in); i++ {
			if in[i] == 0x07 {
				return i + 1, s.osc(in[2:i])
			}
			if in[i] == 0x1b && i+1 < len(in) && in[i+1] == '\\' {
				return i + 2, s.osc(in[2:i])
			}
		}
		return 0, nil
	case '(', ')', '*', '+', '#', '%', ' ':
		if len(in) < 3 {
			return 0, nil
		}
		return 3, nil
	case '7':
		s.savedRow, s.savedCol = s.row, s.col
	case '8':
		s.row, s.col, s.pendingWrap = s.savedRow, s.savedCol, false
	case 'D':
		s.lineFeed()
	case 'E':
		s.col = 0
		s.lineFeed()
	case 'M':
		if s.row > 0 {
			s.row--
		} else {
			s.scrollDown(1)
		}
	}
	return 2, nil
}

// osc answers a color query (OSC 10/11 ;?) with black on white.
func (s *vtScreen) osc(body string) []byte {
	switch body {
	case "10;?":
		return []byte("\x1b]10;rgb:0000/0000/0000\x1b\\")
	case "11;?":
		return []byte("\x1b]11;rgb:ffff/ffff/ffff\x1b\\")
	}
	return nil
}

func (s *vtScreen) csi(in string) (int, []byte) {
	end := 2
	for end < len(in) && (in[end] < 0x40 || in[end] > 0x7e) {
		end++
	}
	if end == len(in) {
		return 0, nil
	}
	body, final := in[2:end], in[end]
	private := ""
	if body != "" && strings.ContainsRune("?<>=", rune(body[0])) {
		private, body = body[:1], body[1:]
	}
	intermediate := ""
	if i := strings.IndexAny(body, " !\"#$%&'()*+,-./"); i >= 0 {
		intermediate, body = body[i:], body[:i]
	}
	params := strings.Split(body, ";")
	arg := func(i, def int) int {
		if i >= len(params) {
			return def
		}
		n, err := strconv.Atoi(params[i])
		if err != nil || n == 0 {
			return def
		}
		return n
	}
	n := arg(0, 1)
	var reply []byte
	if intermediate == "$" && final == 'p' && private == "?" { // DECRQM: not recognized
		return end + 1, fmt.Appendf(nil, "\x1b[?%s;0$y", body)
	}
	if private != "" || intermediate != "" {
		if private == "" && final == 'c' || private == ">" && final == 'c' {
			reply = []byte("\x1b[?62c")
		}
		return end + 1, reply
	}
	switch final {
	case 'A':
		s.row = max(s.row-n, 0)
	case 'B':
		s.row = min(s.row+n, s.height-1)
	case 'C':
		s.col = min(s.col+n, s.width-1)
	case 'D':
		s.col = max(s.col-n, 0)
	case 'E':
		s.row, s.col = min(s.row+n, s.height-1), 0
	case 'F':
		s.row, s.col = max(s.row-n, 0), 0
	case 'G':
		s.col = min(n-1, s.width-1)
	case 'H', 'f':
		s.row, s.col = min(arg(0, 1)-1, s.height-1), min(arg(1, 1)-1, s.width-1)
	case 'd':
		s.row = min(n-1, s.height-1)
	case 'K':
		s.eraseLine(arg(0, 0))
	case 'J':
		s.eraseDisplay(arg(0, 0))
	case 'S':
		s.scrollUp(n)
	case 'T':
		s.scrollDown(n)
	case 'n':
		switch body {
		case "6":
			reply = fmt.Appendf(nil, "\x1b[%d;%dR", s.row+1, s.col+1)
		case "5":
			reply = []byte("\x1b[0n")
		}
	case 'c':
		reply = []byte("\x1b[?62c")
	}
	if final != 'm' {
		s.pendingWrap = false
	}
	return end + 1, reply
}

func (s *vtScreen) eraseLine(mode int) {
	from, to := s.col, s.width
	switch mode {
	case 1:
		from, to = 0, s.col+1
	case 2:
		from = 0
	}
	for k := from; k < min(to, s.width); k++ {
		s.cells[s.row][k] = ""
	}
}

func (s *vtScreen) eraseDisplay(mode int) {
	switch mode {
	case 0:
		s.eraseLine(0)
		for r := s.row + 1; r < s.height; r++ {
			s.cells[r] = s.blankRow()
		}
	case 1:
		s.eraseLine(1)
		for r := 0; r < s.row; r++ {
			s.cells[r] = s.blankRow()
		}
	case 2, 3:
		for r := range s.cells {
			s.cells[r] = s.blankRow()
		}
	}
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
