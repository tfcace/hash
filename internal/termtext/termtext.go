// Package termtext draws untrusted text (buffer contents, history, agent
// output, completion candidates) on a terminal without letting it drive the
// terminal, and measures it the way it is drawn.
package termtext

import (
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// TabWidth is how many cells a tab is drawn as. A fixed width keeps the
// drawing and cursor math in step; expanding to the terminal's tab stops
// would make a character's width depend on its column.
const TabWidth = 4

// Inert is s as it is drawn: control characters show in caret notation (ESC
// as ^[), a tab as TabWidth spaces, and C1 controls and invalid UTF-8 as
// U+FFFD. Every width measured for such text is the width of this.
func Inert(s string) string {
	if isPlain(s) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == '\t':
			b.WriteString(strings.Repeat(" ", TabWidth))
		case r < 0x20:
			b.WriteByte('^')
			b.WriteRune('@' + r)
		case r == 0x7f:
			b.WriteString("^?")
		case r == utf8.RuneError && size == 1, r >= 0x80 && r <= 0x9f:
			b.WriteRune(utf8.RuneError)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// lineBreak marks where a multi-line text's lines meet when drawn on one row.
const lineBreak = " ↵ "

// OneLine is s drawn inert on a single row: its lines are joined with a
// visible return mark, for lists where each entry is one row.
func OneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return Inert(strings.ReplaceAll(s, "\n", lineBreak))
}

// isPlain reports whether s draws as itself: valid UTF-8 with no control
// characters.
func isPlain(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == 0x7f || c == 0xc2 {
			return false // 0xc2 leads the C1 controls; Inert checks them properly
		}
	}
	return utf8.ValidString(s)
}

// Width is how many cells s takes when drawn (Inert), measured by grapheme
// cluster like the terminal.
func Width(s string) int {
	return runewidth.StringWidth(Inert(s))
}

// ClusterEnd is the byte offset just past the grapheme cluster (a character
// with its combining marks, a ZWJ emoji sequence, a flag) that starts at i.
// Measure text per cluster, never per rune, to agree with Width.
func ClusterEnd(s string, i int) int {
	cluster, _, _, _ := uniseg.FirstGraphemeClusterInString(s[i:], -1)
	return i + max(len(cluster), 1)
}

// Truncate shortens s (drawn inert) to at most width cells, ending with
// tail when something was cut. Clusters are never split.
func Truncate(s string, width int, tail string) string {
	s = Inert(s)
	if runewidth.StringWidth(s) <= width {
		return s
	}
	budget := width - runewidth.StringWidth(tail)
	if budget < 0 {
		return ""
	}
	used, end := 0, 0
	for end < len(s) {
		next := ClusterEnd(s, end)
		w := runewidth.StringWidth(s[end:next])
		if used+w > budget {
			break
		}
		used, end = used+w, next
	}
	return s[:end] + tail
}
