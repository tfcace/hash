package markdown

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tfcace/hash/internal/termtext"
)

// splitCorpus holds replies whose chunk boundaries broke rendering in a live
// session: a heading styled only up to the split, a fence language printed
// as text, a closing fence printed literally (leaving the rest of the reply
// in code style), and a bullet left as a raw dash.
var splitCorpus = []string{
	"## Hash at a glance\n\nHash is a Go shell.\n",
	"Intro\n\n```go\npackage main\nfunc main() {}\n```\n\n- Input\n  - Keybindings\n    - Helix\n\nafter\n",
	"• first\n- Tab: a real tab\n* star item\n12. twelfth\n",
	"# H1\n### H3 with `code`\n#### H4 **bold**\n",
	"Run `jj log -r @` then **check** it.\n",
	"```\n`not a fence`\n``still code``\n```\ndone\n",
	"  - indented item\n\t- tab item\n",
	"#include is not a heading\n- **bold** item with `code`\n",
}

func renderWhole(doc string) string {
	r := NewStreamingRenderer()
	return r.Write(doc) + r.Finish()
}

// renderSplit streams doc as two chunks with a partial-line Flush between
// them, the way the agent stream engine flushes after every text event.
func renderSplit(doc string, at int) string {
	r := NewStreamingRenderer()
	var out strings.Builder
	out.WriteString(r.Write(doc[:at]))
	out.WriteString(r.Flush())
	out.WriteString(r.Write(doc[at:]))
	out.WriteString(r.Finish())
	return out.String()
}

// A streamed reply must render the same however the transport chunks it.
func TestStreamingRenderer_ChunkSplitInvariant(t *testing.T) {
	for _, doc := range splitCorpus {
		want := renderWhole(doc)
		for at := 1; at < len(doc); at++ {
			if !utf8.RuneStart(doc[at]) {
				continue
			}
			if got := renderSplit(doc, at); got != want {
				t.Errorf("split %q | %q\n got: %q\nwant: %q", doc[:at], doc[at:], got, want)
				break // one report per document is enough
			}
		}
	}
}

// A line start is indented by spaces and tabs only. Other white space (a
// carriage return, a form feed) before a number made it a list item when
// the line arrived whole and plain text when it was split there.
func TestStreamingRenderer_OnlySpacesAndTabsIndentAListItem(t *testing.T) {
	for _, doc := range []string{"\r2. twelfth\n", "\f\f0. 0", "\v1. one\n"} {
		want := renderWhole(doc)
		if want != doc {
			t.Errorf("%q is drawn as a list item: %q", doc, want)
		}
		for at := 1; at < len(doc); at++ {
			if got := renderSplit(doc, at); got != want {
				t.Errorf("split %q | %q\n got: %q\nwant: %q", doc[:at], doc[at:], got, want)
			}
		}
	}
	if got := renderWhole(" \t3. three\n"); !strings.Contains(got, cyan+"3."+reset) {
		t.Errorf("an indented item lost its marker style: %q", got)
	}
}

// replyText is doc as the renderer can receive it: a reply reaches it
// through termtext.TextFilter, which leaves text, line feeds and tabs.
func replyText(doc string) string {
	var f termtext.TextFilter
	return f.Write(doc)
}

// FuzzStreamingRendererSplit checks the same invariant on any text a reply
// can hold.
func FuzzStreamingRendererSplit(f *testing.F) {
	for _, doc := range splitCorpus {
		f.Add(doc, uint(len(doc)/2))
	}
	f.Fuzz(func(t *testing.T, doc string, at uint) {
		if doc = replyText(doc); len(doc) < 2 {
			return
		}
		i := 1 + int(at%uint(len(doc)-1))
		if !utf8.RuneStart(doc[i]) {
			return
		}
		if got, want := renderSplit(doc, i), renderWhole(doc); got != want {
			t.Errorf("split %q | %q\n got: %q\nwant: %q", doc[:i], doc[i:], got, want)
		}
	})
}
