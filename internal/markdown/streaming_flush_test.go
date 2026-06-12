package markdown

import (
	"regexp"
	"strings"
	"testing"
)

var ansiSeqRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripAnsi(s string) string {
	return ansiSeqRe.ReplaceAllString(s, "")
}

// A code-block line split by a partial-line Flush must not get the
// two-space indent injected again mid-line ("wc -l" became "wc -  l").
func TestStreamingRenderer_FlushDoesNotReindentCodeLine(t *testing.T) {
	r := NewStreamingRenderer()
	var out strings.Builder

	out.WriteString(r.Write("```\n"))
	out.WriteString(r.Write("find . -type"))
	out.WriteString(r.Flush())
	out.WriteString(r.Write(" f -mmin -60\n"))

	got := stripAnsi(out.String())
	want := "\n  find . -type f -mmin -60\n"
	if got != want {
		t.Errorf("rendered = %q, want %q", got, want)
	}
}

// Inline code split across a Flush must keep one styled span instead of
// closing and reopening (which inverted the backtick state).
func TestStreamingRenderer_FlushKeepsInlineCodeOpen(t *testing.T) {
	r := NewStreamingRenderer()
	var out strings.Builder

	out.WriteString(r.Write("run `wc -"))
	out.WriteString(r.Flush())
	out.WriteString(r.Write("l` now\n"))

	if got, want := stripAnsi(out.String()), "run wc -l now\n"; got != want {
		t.Errorf("rendered = %q, want %q", got, want)
	}
	if n := strings.Count(out.String(), cyan); n != 1 {
		t.Errorf("inline code opened %d times, want 1", n)
	}
}

// A continuation fragment that happens to start like an ordered list
// must not be decorated as one.
func TestStreamingRenderer_ContinuationNotMisreadAsList(t *testing.T) {
	r := NewStreamingRenderer()
	var out strings.Builder

	out.WriteString(r.Write("version "))
	out.WriteString(r.Flush())
	out.WriteString(r.Write("1. 2 done\n"))

	if got, want := stripAnsi(out.String()), "version 1. 2 done\n"; got != want {
		t.Errorf("rendered = %q, want %q", got, want)
	}
	if strings.Contains(out.String(), cyan) {
		t.Error("continuation fragment was styled as a list item")
	}
}

// A continuation fragment that happens to start like a header must not
// be decorated (header styling also strips the marker, losing text).
func TestStreamingRenderer_ContinuationNotMisreadAsHeader(t *testing.T) {
	r := NewStreamingRenderer()
	var out strings.Builder

	out.WriteString(r.Write("see "))
	out.WriteString(r.Flush())
	out.WriteString(r.Write("# 1 below\n"))

	if got, want := stripAnsi(out.String()), "see # 1 below\n"; got != want {
		t.Errorf("rendered = %q, want %q", got, want)
	}
}

// Finish must close styling that a never-completed line left open, so
// stream end can't bleed color into the prompt.
func TestStreamingRenderer_FinishClosesOpenStyling(t *testing.T) {
	r := NewStreamingRenderer()
	var out strings.Builder

	out.WriteString(r.Write("```\n"))
	out.WriteString(r.Write("ls -l")) // stream ends mid code line

	got := r.Finish()
	out.WriteString(got)

	if !strings.HasSuffix(got, reset) {
		t.Errorf("Finish output %q does not close styling with reset", got)
	}
	if want := "\n  ls -l"; stripAnsi(out.String()) != want {
		t.Errorf("rendered = %q, want %q", stripAnsi(out.String()), want)
	}
}
