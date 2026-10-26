package markdown

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func wrapAll(width int, text string) string {
	w := NewWrapper(width)
	return w.Write(text) + w.Flush()
}

func TestWrapper_BreaksAtSpaces(t *testing.T) {
	got := wrapAll(10, "aaa bbb ccc ddd\n")
	if want := "aaa bbb\nccc ddd\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWrapper_EscapesHaveNoWidth(t *testing.T) {
	got := stripAnsi(wrapAll(10, "\x1b[1maaa\x1b[0m bbb ccc\n"))
	if want := "aaa bbb\nccc\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A wrapped bullet continues under its text, not under the bullet.
func TestWrapper_HangingIndentForBullets(t *testing.T) {
	got := stripAnsi(wrapAll(12, "  \x1b[36m•\x1b[0m one two three four\n"))
	if want := "  • one two\n    three\n    four\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	got = stripAnsi(wrapAll(12, "\x1b[36m12.\x1b[0m one two three\n"))
	if want := "12. one two\n    three\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A word longer than the line is broken where the line ends rather than
// left for the terminal to break.
func TestWrapper_HardBreaksLongWords(t *testing.T) {
	got := wrapAll(5, "abcdefghijklm\n")
	if want := "abcde\nfghij\nklm\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A styled span cut by a wrap resumes its style on the next row, so a
// prefix written at the row start (the conversation rail) cannot end it.
func TestWrapper_ReappliesStyleAfterBreak(t *testing.T) {
	got := wrapAll(8, "\x1b[36mone two three\x1b[0m\n")
	want := "\x1b[36mone two\x1b[0m\n\x1b[36mthree\x1b[0m\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWrapper_WideRunesCountTwoColumns(t *testing.T) {
	got := wrapAll(6, "日本 語テ\n")
	if want := "日本\n語テ\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWrapper_ZeroWidthPassesThrough(t *testing.T) {
	text := "a very long line that would otherwise wrap\n"
	if got := wrapAll(0, text); got != text {
		t.Errorf("got %q, want %q", got, text)
	}
}

func TestWrapper_KeepsLeadingIndentation(t *testing.T) {
	got := stripAnsi(wrapAll(14, "  func main() { return }\n"))
	if want := "  func main()\n  { return }\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

var wrapCorpus = []string{
	"aaa bbb ccc ddd eee fff\n",
	"  \x1b[36m•\x1b[0m one \x1b[1mtwo three\x1b[0m four five six\n",
	"日本語テキスト 🚀👍🏽 https://example.com/a/very/long/path\n",
	"\x1b[90m  \tcode with\ttabs and words\x1b[0m\n",
	"abcdefghijklmnopqrstuvwxyz abc\n",
}

// The wrapper sees the same streamed fragments as the renderer: its output
// must not depend on where they split.
func TestWrapper_ChunkSplitInvariant(t *testing.T) {
	for _, doc := range wrapCorpus {
		for _, width := range []int{5, 8, 13, 20} {
			want := wrapAll(width, doc)
			for at := 1; at < len(doc); at++ {
				if !utf8.RuneStart(doc[at]) {
					continue
				}
				w := NewWrapper(width)
				got := w.Write(doc[:at]) + w.Write(doc[at:]) + w.Flush()
				if got != want {
					t.Errorf("width %d split %q | %q\n got: %q\nwant: %q", width, doc[:at], doc[at:], got, want)
					break
				}
			}
		}
	}
}

// No row may be wider than the wrap width.
func TestWrapper_RowsFitWidth(t *testing.T) {
	for _, doc := range wrapCorpus {
		for _, width := range []int{5, 8, 13, 20} {
			for _, row := range strings.Split(stripAnsi(wrapAll(width, doc)), "\n") {
				if n := displayWidth(row); n > width {
					t.Errorf("width %d: row %q is %d columns", width, row, n)
				}
			}
		}
	}
}

// FuzzWrapper checks split independence and row width on arbitrary input.
func FuzzWrapper(f *testing.F) {
	for _, doc := range wrapCorpus {
		f.Add(doc, uint8(8), uint(len(doc)/2))
	}
	f.Fuzz(func(t *testing.T, doc string, width uint8, at uint) {
		if !utf8.ValidString(doc) || len(doc) < 2 || width == 0 {
			return
		}
		w := int(width)
		i := 1 + int(at%uint(len(doc)-1))
		if !utf8.RuneStart(doc[i]) {
			return
		}
		want := wrapAll(w, doc)
		wr := NewWrapper(w)
		if got := wr.Write(doc[:i]) + wr.Write(doc[i:]) + wr.Flush(); got != want {
			t.Fatalf("split changes output\n got: %q\nwant: %q", got, want)
		}
		if strings.ContainsAny(doc, "\x1b\r\v\f\b\x00") {
			return // control bytes have no defined width
		}
		for _, row := range strings.Split(stripAnsi(want), "\n") {
			if n := displayWidth(row); n > w && utf8.RuneCountInString(row) > 1 {
				t.Fatalf("width %d: row %q is %d columns", w, row, n)
			}
		}
	})
}
