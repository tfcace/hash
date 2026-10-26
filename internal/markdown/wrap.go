package markdown

import (
	"regexp"
	"strings"

	"github.com/mattn/go-runewidth"
)

// tabWidth is how many columns a tab in a reply expands to. The wrapper
// expands tabs itself: a terminal aligns them to absolute columns, which a
// rail prefix or a wrap would shift.
const tabWidth = 4

// wrapToken is one unit of a pending word: a printable rune with its width,
// or an escape sequence with none.
type wrapToken struct {
	text  string
	width int
}

// Wrapper word-wraps rendered ANSI text to a column width as it streams in.
// Hash breaks lines itself so it knows how many rows a reply takes and can
// indent continuations; left to the terminal, a long line breaks mid-word at
// column 0 and escapes any prefix drawn at the row start.
type Wrapper struct {
	width int

	col     int             // columns used on the current row
	line    strings.Builder // visible text of the logical line, for its indent
	indent  int             // continuation indent of the logical line
	measure bool            // indent not yet measured for this line

	spaces    int // pending spaces before the next word
	word      []wrapToken
	wordWidth int

	esc   strings.Builder // escape sequence being read
	inEsc bool

	sgr string // styling active at the output position
}

// NewWrapper creates a wrapper for the given width. A width below 1 passes
// text through unchanged.
func NewWrapper(width int) *Wrapper {
	return &Wrapper{width: width, measure: true}
}

// Write consumes a fragment and returns the text that is ready to draw. The
// word in progress is held until it ends, so it can move to the next row
// whole.
func (w *Wrapper) Write(text string) string {
	if w.width < 1 {
		return text
	}
	var out strings.Builder
	for _, r := range text {
		if w.inEsc {
			w.esc.WriteRune(r)
			if escapeDone(w.esc.String()) {
				w.inEsc = false
				w.word = append(w.word, wrapToken{text: w.esc.String()})
				w.esc.Reset()
			}
			continue
		}
		switch r {
		case '\x1b':
			w.inEsc = true
			w.esc.WriteRune(r)
		case '\n':
			w.flushWord(&out)
			w.spaces = 0
			w.emit(&out, "\n")
			w.col = 0
			w.line.Reset()
			w.measure = true
		case ' ', '\t':
			w.flushWord(&out)
			n := 1
			if r == '\t' {
				n = tabWidth - (w.col+w.spaces)%tabWidth
			}
			w.spaces += n
			w.noteLine(strings.Repeat(" ", n))
		default:
			rw := runewidth.RuneWidth(r)
			w.word = append(w.word, wrapToken{text: string(r), width: rw})
			w.wordWidth += rw
			w.noteLine(string(r))
		}
	}
	return out.String()
}

// Flush draws the word in progress. Call at a boundary the reply cannot
// continue across (a tool row, the end of the stream).
func (w *Wrapper) Flush() string {
	if w.width < 1 {
		return ""
	}
	var out strings.Builder
	w.flushWord(&out)
	w.spaces = 0
	return out.String()
}

// noteLine records the start of the logical line, enough to measure its
// indent (leading spaces plus a list marker).
func (w *Wrapper) noteLine(s string) {
	if w.measure && w.line.Len() < 32 {
		w.line.WriteString(s)
	}
}

// flushWord draws the pending spaces and word, starting a new row first
// when the word does not fit on this one.
func (w *Wrapper) flushWord(out *strings.Builder) {
	if len(w.word) == 0 {
		return
	}
	if w.col > 0 && w.col+w.spaces+w.wordWidth > w.width {
		w.breakRow(out)
	} else {
		// Indentation alone must leave a column for the text.
		w.spaces = min(w.spaces, w.width-w.col-1)
		w.emit(out, strings.Repeat(" ", w.spaces))
		w.col += w.spaces
	}
	w.spaces = 0

	for _, tok := range w.word {
		if tok.width > 0 && w.col+tok.width > w.width && w.col > w.rowStart() {
			w.breakRow(out)
		}
		w.emit(out, tok.text)
		w.col += tok.width
	}
	w.word = w.word[:0]
	w.wordWidth = 0
}

// breakRow ends the row and starts the next at the line's indent, with the
// active style closed before the break and reopened after it.
func (w *Wrapper) breakRow(out *strings.Builder) {
	if w.measure {
		w.indent = lineIndent(w.line.String(), w.width)
		w.measure = false
	}
	style := w.sgr
	if style != "" {
		w.emit(out, reset)
	}
	w.emit(out, "\n"+strings.Repeat(" ", w.indent)+style)
	w.col = w.indent
}

// rowStart is the column a continuation row starts at.
func (w *Wrapper) rowStart() int {
	if w.measure {
		return 0
	}
	return w.indent
}

// emit writes s and tracks the SGR state it leaves behind.
func (w *Wrapper) emit(out *strings.Builder, s string) {
	out.WriteString(s)
	for _, seq := range sgrRe.FindAllString(s, -1) {
		if seq == reset || seq == "\x1b[m" {
			w.sgr = ""
		} else {
			w.sgr += seq
		}
	}
}

var sgrRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// orderedMarkerRe matches an ordered list marker with its space.
var orderedMarkerRe = regexp.MustCompile(`^\d+\. `)

// lineIndent is where a wrapped line continues: under the text of a list
// item, under the first character of an indented line. An indent that would
// leave too little room is dropped.
func lineIndent(line string, width int) int {
	rest := strings.TrimLeft(line, " ")
	indent := len(line) - len(rest)
	switch {
	case strings.HasPrefix(rest, "• "):
		indent += 2
	case orderedMarkerRe.MatchString(rest):
		indent += len(orderedMarkerRe.FindString(rest))
	}
	if indent > width/2 {
		return 0
	}
	return indent
}

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

// displayWidth is the number of columns s occupies.
func displayWidth(s string) int {
	return runewidth.StringWidth(s)
}
