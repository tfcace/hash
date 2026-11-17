package termtext

import (
	"strings"
	"unicode/utf8"
)

// TextFilter turns text from a source that is not trusted with the terminal
// (an agent's reply) into text and nothing else, as it streams in. Escape
// sequences are removed whole, so none of their bytes is left as text. Of
// the control characters only the line feed and the tab stay: a carriage
// return or backspace could rewrite what was already shown. An invalid byte
// becomes U+FFFD. Whatever the input, the output cannot drive the terminal.
//
// The zero value is ready to use. What a chunk ends inside (a sequence, a
// character) is held until the next chunk completes it; at the end of the
// stream it is dropped.
type TextFilter struct {
	held string
}

// Write returns the text of chunk, after anything held from the chunks
// before it.
func (f *TextFilter) Write(chunk string) string {
	if f.held == "" && isPlain(chunk) {
		return chunk
	}
	s := f.held + chunk
	f.held = ""

	var out strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			n, ended := escapeIn(s[i:])
			if !ended {
				f.held = s[i:]
				break
			}
			i += n
			continue
		}
		if !utf8.FullRuneInString(s[i:]) {
			f.held = s[i:]
			break
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == utf8.RuneError && size == 1:
			out.WriteRune(utf8.RuneError)
		case r == '\n', r == '\t':
			out.WriteRune(r)
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

// escapeIn is the length of the escape sequence s starts with, and whether
// s holds all of it. It reads a sequence strictly: a byte that cannot be
// part of it ends it there and stays text, and no sequence runs past the
// end of its line. The terminal may read a malformed sequence differently,
// which no longer matters once its ESC is gone.
func escapeIn(s string) (n int, ended bool) {
	if len(s) < 2 {
		return len(s), false
	}
	switch c := s[1]; {
	case c == '[':
		// CSI: parameter and intermediate bytes, then a final byte
		return sequenceIn(s, 2, 0x20, 0x3f, 0x40)
	case strings.IndexByte("]PX^_", c) >= 0:
		// A string sequence: up to ST, or BEL for OSC
		n = EscapeLen(s)
		if line := strings.IndexByte(s[:n], '\n'); line >= 0 {
			return line, true
		}
		return n, n < len(s) || strings.HasSuffix(s, "\x1b\\") || (c == ']' && s[n-1] == 0x07)
	}
	// ESC, its intermediate bytes, and a final byte
	return sequenceIn(s, 1, 0x20, 0x2f, 0x30)
}

// sequenceIn reads a sequence of s from its byte at: bytes from lo to hi,
// then one final byte from final to 0x7e. Any other byte (a second ESC, a
// control, part of a wide character) ends the sequence before it.
func sequenceIn(s string, at int, lo, hi, final byte) (n int, ended bool) {
	for at < len(s) && s[at] >= lo && s[at] <= hi {
		at++
	}
	switch {
	case at == len(s):
		return at, false
	case s[at] >= final && s[at] <= 0x7e:
		return at + 1, true
	}
	return at, true
}
