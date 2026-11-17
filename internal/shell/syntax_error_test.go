package shell

import (
	"bytes"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func parseError(t *testing.T, src string) error {
	t.Helper()
	_, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(src), "")
	if err == nil {
		t.Fatalf("%q parsed", src)
	}
	return err
}

func syntaxBanner(t *testing.T, cols int, src string) string {
	t.Helper()
	h, buf := bannerHandler(t)
	withColumns(t, cols)
	if !h.HandleSyntaxError(src, parseError(t, src)) {
		t.Fatalf("%q: not drawn as a syntax error", src)
	}
	return stripANSI(buf.String())
}

// A syntax error shows the line it is in and points at the column, so the
// place to fix is on screen instead of in a "1:17" to count out.
func TestSyntaxError_PointsAtTheColumn(t *testing.T) {
	got := syntaxBanner(t, 100, "echo $(( 1 + ); echo hi")
	want := "\n" +
		"┌ ✗ syntax error · line 1\n" +
		"│ echo $(( 1 + ); echo hi\n" +
		"│ " + strings.Repeat(" ", strings.Index("echo $(( 1 + )", "+")) + "┬\n" +
		"│ " + strings.Repeat(" ", strings.Index("echo $(( 1 + )", "+")) + "╰ `+` must be followed by an expression\n" +
		"└─ ↑ to edit · ?? to explain\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestSyntaxError_ShowsTheLineTheErrorIsOn(t *testing.T) {
	got := syntaxBanner(t, 100, "echo one\necho $(( 2 + )\necho three")
	for _, want := range []string{"┌ ✗ syntax error · line 2\n", "│ echo $(( 2 + )\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "echo one") || strings.Contains(got, "echo three") {
		t.Errorf("other lines are shown:\n%s", got)
	}
}

func TestSyntaxError_ColorsTheTitleAndThePointer(t *testing.T) {
	h, buf := bannerHandler(t)
	h.HandleSyntaxError("echo $(( 1 + )", parseError(t, "echo $(( 1 + )"))
	for _, want := range []string{ansiBold + bannerError + "✗ syntax error", bannerError + "┬", bannerError + "╰ "} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("no %q in %q", want, buf.String())
		}
	}
}

// The pointer counts columns, not bytes: wide and multibyte characters
// before it must not push it off its target.
func TestSyntaxError_PointsPastWideCharacters(t *testing.T) {
	rows := strings.Split(syntaxBanner(t, 100, "echo 日本 $(( 1 + )"), "\n")
	line, pointer := rows[2], rows[3]
	if at := strings.Index(line, "+"); visibleCols(line[:at]) != visibleCols(strings.TrimSuffix(pointer, "┬")) {
		t.Errorf("pointer is off its target:\n%s\n%s", line, pointer)
	}
}

// No row may wrap: a long line is cut around the pointer.
func TestSyntaxError_FitsALongLineAroundThePointer(t *testing.T) {
	src := "echo " + strings.Repeat("word ", 30) + "$(( 1 + ); echo " + strings.Repeat("tail ", 30)
	got := syntaxBanner(t, 60, src)
	assertRowsFit(t, got, 60)
	rows := strings.Split(got, "\n")
	line, pointer := rows[2], rows[3]
	if !strings.HasPrefix(line, "│ …") || !strings.HasSuffix(line, "…") {
		t.Errorf("the line is not cut on both sides: %q", line)
	}
	under := []rune(line)[len([]rune(pointer))-1]
	if under != '+' {
		t.Errorf("pointer is under %q, want the `+`:\n%s\n%s", under, line, pointer)
	}
}

func TestSyntaxError_DrawsTheLineInert(t *testing.T) {
	h, buf := bannerHandler(t)
	src := "echo \x1b]0;pwned\x07 $(( 1 + )"
	h.HandleSyntaxError(src, parseError(t, src))
	if strings.Contains(buf.String(), "\x1b]0;pwned") || strings.Contains(buf.String(), "\x07") {
		t.Errorf("the line is drawn raw: %q", buf.String())
	}
}

func TestSyntaxError_IsNotDrawnForAnyOtherError(t *testing.T) {
	var buf bytes.Buffer
	h := &ErrorHandler{out: &buf}
	if h.HandleSyntaxError("ls", bytes.ErrTooLarge) || buf.Len() != 0 {
		t.Errorf("drew %q for an error that is not a syntax error", buf.String())
	}
}
