package markdown

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// streamPieces are markdown fragments a reply streams in; the seeds are
// built from them.
var streamPieces = []string{"**", "*", "`", "```", "```go", "# ", "## ", "- ", "* ", "1. ", "12", ".", " ", "\n", "abc", "é", "日本", "\t", "  ", "x"}

// chunks splits doc into pieces whose rune lengths come from sizes (1-4
// runes each, cycling), the rest in one piece.
func chunks(doc string, sizes []byte) []string {
	rs := []rune(doc)
	var out []string
	for i, k := 0, 0; i < len(rs); k++ {
		n := len(rs) - i
		if len(sizes) > 0 {
			n = min(1+int(sizes[k%len(sizes)]%4), len(rs)-i)
		}
		out = append(out, string(rs[i:i+n]))
		i += n
	}
	return out
}

// streamFlushed renders doc chunk by chunk, flushing after each chunk the
// way the agent stream does between deltas.
func streamFlushed(doc string, sizes []byte) string {
	r := NewStreamingRenderer()
	var out strings.Builder
	for _, c := range chunks(doc, sizes) {
		out.WriteString(r.Write(c) + r.Flush())
	}
	return out.String() + r.Finish()
}

// openStyleAtNewline reports the first line that ends with an SGR style
// still open, or -1.
func openStyleAtNewline(s string) int {
	open, line := false, 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' {
			if m := ansiSeqRe.FindString(s[i:]); m != "" {
				open = m != reset
				i += len(m) - 1
				continue
			}
		}
		if s[i] == '\n' {
			if open {
				return line
			}
			line++
		}
	}
	return -1
}

func streamSeeds(f *testing.F, add func(doc string)) {
	for i := range 64 {
		var b strings.Builder
		for j := range 1 + i%12 {
			b.WriteString(streamPieces[(i*7+j*3)%len(streamPieces)])
		}
		add(b.String())
	}
}

// However a reply is chunked and flushed, it reads the same as rendered
// whole, and no line ends with a style left open (it would bleed into the
// next row or the prompt).
func FuzzStreamingChunkedFlush(f *testing.F) {
	streamSeeds(f, func(doc string) { f.Add(doc, []byte{1, 3, 0, 2}) })
	f.Fuzz(func(t *testing.T, doc string, sizes []byte) {
		if !utf8.ValidString(doc) {
			return
		}
		got := streamFlushed(doc, sizes)
		if want := renderWhole(doc); stripAnsi(got) != stripAnsi(want) {
			t.Fatalf("chunked text differs for %q\n got: %q\nwant: %q", doc, stripAnsi(got), stripAnsi(want))
		}
		if line := openStyleAtNewline(got); line >= 0 {
			t.Fatalf("style open at the end of line %d for %q: %q", line, doc, got)
		}
	})
}

// The wrapper's output does not depend on how its input is chunked.
func FuzzWrapperChunked(f *testing.F) {
	streamSeeds(f, func(doc string) { f.Add(renderWhole(doc), uint8(8), []byte{2, 0, 1}) })
	f.Fuzz(func(t *testing.T, doc string, width uint8, sizes []byte) {
		if !utf8.ValidString(doc) || width == 0 {
			return
		}
		want := wrapAll(int(width), doc)
		w := NewWrapper(int(width))
		var got strings.Builder
		for _, c := range chunks(doc, sizes) {
			got.WriteString(w.Write(c))
		}
		got.WriteString(w.Flush())
		if got.String() != want {
			t.Fatalf("width %d: chunked output differs for %q\n got: %q\nwant: %q", width, doc, got.String(), want)
		}
	})
}
