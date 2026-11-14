package completion

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

// BenchmarkBuiltinPluginSpecs measures the cost of producing the built-in
// specs, paid on every handler-table rebuild (startup and completions reload).
func BenchmarkBuiltinPluginSpecs(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if specs := builtinPluginSpecs(); len(specs) == 0 {
			b.Fatal("no built-in specs")
		}
	}
}

// BenchmarkNewPluginCompleter measures completer construction as done at
// shell startup, built-in spec handling included.
func BenchmarkNewPluginCompleter(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if c := NewPluginCompleter(nil); c == nil {
			b.Fatal("nil completer")
		}
	}
}

// BenchmarkWriteShellText measures writing a full menu (200 items, the
// limit) with every completed line read back by insertionCheck, in a few
// places a line can end. The check's budget is well under 10ms a menu.
func BenchmarkWriteShellText(b *testing.B) {
	items := make([]Item, completionItemLimit)
	for i := range items {
		items[i] = Item{Value: fmt.Sprintf("file %d;(x).txt", i)}
	}
	for _, line := range []string{
		"git add src/",
		`cat "src/`,
		"for f in a b; do echo `ls src/",
		"cat <<EOF\nsee src/",
		"if true; then x=$(echo \"${y:-src/",
	} {
		b.Run(fmt.Sprintf("%q", line), func(b *testing.B) {
			result := Result{Items: items, ReplaceFrom: len(line)}
			for b.Loop() {
				if got, _ := writeShellText(context.Background(), result, line, len(line), syntax.LangBash); len(got.Items) != len(items) {
					b.Fatalf("%d of %d items kept", len(got.Items), len(items))
				}
			}
		})
	}
}

// BenchmarkWriteShellTextLongBuffer is BenchmarkWriteShellText in a long
// buffer (a script pasted or typed over many lines), the cursor at its
// start, middle or end: only the statements around the insertion are read
// for each item, so a menu should cost what it costs on its own line. A
// statement that is long itself (a command continued over a thousand
// lines, a heredoc body) is read whole for each item, until the completion
// deadline.
func BenchmarkWriteShellTextLongBuffer(b *testing.B) {
	items := make([]Item, completionItemLimit)
	for i := range items {
		items[i] = Item{Value: fmt.Sprintf("file %d;(x).txt", i)}
	}
	script := func(lines int) []string {
		var script []string
		for i := range lines {
			script = append(script, fmt.Sprintf("echo \"line %d of the script\" | grep -v foo > /dev/null", i))
		}
		return script
	}
	type at struct{ name, before, after string }
	var cases []at
	for _, lines := range []int{100, 1000} {
		s := script(lines)
		cases = append(cases,
			at{fmt.Sprintf("lines=%d/start", lines), "cat src/", "\n" + strings.Join(s, "\n")},
			at{fmt.Sprintf("lines=%d/middle", lines), strings.Join(s[:lines/2], "\n") + "\ncat src/", "\n" + strings.Join(s[lines/2:], "\n")},
			at{fmt.Sprintf("lines=%d/end", lines), strings.Join(s, "\n") + "\ncat src/", ""})
	}
	cases = append(cases,
		at{"continued=1000", "docker run \\\n" + strings.Repeat("  --env FOO=bar \\\n", 1000) + "  -v src/", ""},
		at{"heredoc=1000", "cat <<EOF\n" + strings.Repeat("some text line\n", 1000) + "src/", ""})
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			line := c.before + c.after
			result := Result{Items: items, ReplaceFrom: len(c.before)}
			for b.Loop() {
				if got, _ := writeShellText(context.Background(), result, line, len(c.before), syntax.LangBash); len(got.Items) != len(items) {
					b.Fatalf("%d of %d items kept", len(got.Items), len(items))
				}
			}
		})
	}
}
