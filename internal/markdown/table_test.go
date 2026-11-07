package markdown

import (
	"strings"
	"testing"
)

// drawn renders a reply as the shell does, chunk runes at a time, and
// returns it without its styling.
func drawn(t *testing.T, src string, width, chunk int) string {
	t.Helper()
	r := NewStreamingRenderer()
	r.Width = width
	w := NewWrapper(width)
	var out strings.Builder
	rs := []rune(src)
	for len(rs) > 0 {
		n := min(chunk, len(rs))
		out.WriteString(w.Write(r.Write(string(rs[:n])) + r.Flush()))
		rs = rs[n:]
	}
	out.WriteString(w.Write(r.Finish()) + w.Flush())
	return sgrRe.ReplaceAllString(out.String(), "")
}

const dockerTable = `OrbStack is the active Docker daemon.

| Context | Engine | Socket |
|---|---|---|
| orbstack (active) | Docker 29.8.2 | ~/.orbstack/run/docker.sock |
| default | ` + "`/var/run/docker.sock`" + ` | |

Switch with **docker context use**.
`

func TestTable_DrawsAlignedColumns(t *testing.T) {
	want := `OrbStack is the active Docker daemon.

Context            Engine                Socket
─────────────────  ────────────────────  ───────────────────────────
orbstack (active)  Docker 29.8.2         ~/.orbstack/run/docker.sock
default            /var/run/docker.sock

Switch with docker context use.
`
	for _, chunk := range []int{1, 3, 7, 1000} {
		if got := drawn(t, dockerTable, 100, chunk); got != want {
			t.Errorf("chunks of %d:\n%s\nwant:\n%s", chunk, got, want)
		}
	}
}

func TestTable_HeaderIsBoldAndCellsKeepTheirMarkup(t *testing.T) {
	r := NewStreamingRenderer()
	out := r.Write("| Name | Value |\n|---|---|\n| `a` | **b** |\n") + r.Finish()
	for _, want := range []string{bold + "Name" + reset, cyan + "a" + reset, bold + "b" + reset} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%q", want, out)
		}
	}
}

func TestTable_EndsWithTheReply(t *testing.T) {
	got := drawn(t, "| a | b |\n|---|---|\n| 1 | 2 |", 80, 2)
	if want := "a  b\n─  ─\n1  2"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTable_AlignsColumnsAsTheRuleRowSays(t *testing.T) {
	got := drawn(t, "| n | mid | name |\n|--:|:-:|:--|\n| 1 | x | a |\n| 100 | y | b |\n", 80, 1000)
	want := "  n  mid  name\n───  ───  ────\n  1   x   a\n100   y   b\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTable_LinesWithPipesThatAreNoTableStayText(t *testing.T) {
	src := "| not a table\n| still not\nplain\n"
	if got := drawn(t, src, 80, 1); got != src {
		t.Errorf("got %q, want %q", got, src)
	}
}

func TestTable_InsideACodeBlockIsCode(t *testing.T) {
	got := drawn(t, "```\n| a | b |\n|---|---|\n```\n", 80, 1)
	if want := "  | a | b |\n  |---|---|\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTable_AnEscapedPipeIsTheCells(t *testing.T) {
	got := drawn(t, "| cmd |\n|---|\n| a \\| b |\n", 80, 1000)
	if want := "cmd\n─────\na | b\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTable_WrapsCellsToFitTheRow(t *testing.T) {
	src := "| Context | Engine | Socket |\n|---|---|---|\n" +
		"| claude-hash | Claude sandbox VM | ~/.docker/sandboxes/vm/claude-hash/docker-public.sock |\n"
	want := `Context      Engine             Socket
───────────  ─────────────────  ──────────────────
claude-hash  Claude sandbox VM  ~/.docker/
                                sandboxes/vm/
                                claude-hash/
                                docker-public.sock
`
	if got := drawn(t, src, 50, 1000); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// No table row is wider than the pane, at any width: the wrapper would
// break a wider one in the middle of a column.
func TestTable_NeverDrawsARowWiderThanThePane(t *testing.T) {
	src := "| Context | Engine | Socket | Note |\n|---|--:|:-:|---|\n" +
		"| orbstack (active) | Docker 29.8.2, linux/aarch64 | ~/.orbstack/run/docker.sock | `a` **b** |\n" +
		"| 日本語のテキスト | x | averyveryverylongwordwithnoseparatorsatall | |\n"
	for width := 20; width <= 140; width++ {
		for _, row := range strings.Split(drawn(t, src, width, 1000), "\n") {
			if strings.Contains(row, ": ") || !strings.Contains(row, "  ") {
				continue // a record line, wrapped as text
			}
			if w := displayWidth(row); w > width {
				t.Fatalf("width %d: row of %d columns: %q", width, w, row)
			}
		}
	}
}

func TestTable_TooWideForThePaneIsDrawnAsRecords(t *testing.T) {
	src := "| Context | Engine | Socket |\n|---|---|---|\n| orbstack | Docker 29 | docker.sock |\n| default | | var.sock |\n"
	want := `Context: orbstack
Engine: Docker 29
Socket: docker.sock

Context: default
Socket: var.sock
`
	if got := drawn(t, src, 24, 1000); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
