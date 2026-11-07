package shparse

import (
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

// mvdan's zsh parser dereferences nil on a heredoc word like $a[; Parse
// returns an error instead.
func TestParse_ParserPanicIsAnError(t *testing.T) {
	zsh := syntax.NewParser(syntax.Variant(syntax.LangZsh))
	if f, err := Parse(zsh, "cat <<$a[\nx\n", ""); f != nil || err == nil || !strings.Contains(err.Error(), "shell parser failed") {
		t.Errorf("Parse = %v, %v, want a parser failure", f, err)
	}
	if f, err := Parse(syntax.NewParser(), "echo hi\n", ""); err != nil || len(f.Stmts) != 1 {
		t.Errorf("Parse(echo hi) = %v, %v", f, err)
	}
}

func TestStmts(t *testing.T) {
	var lines []uint
	err := Stmts(syntax.NewParser(), strings.NewReader("a\nb; c\n"), func(s *syntax.Stmt) bool {
		lines = append(lines, s.Pos().Line())
		return true
	})
	if err != nil || len(lines) != 3 || lines[2] != 2 {
		t.Errorf("Stmts = %v, %v", lines, err)
	}
	zsh := syntax.NewParser(syntax.Variant(syntax.LangZsh))
	if err := Stmts(zsh, strings.NewReader("cat <<$a[\nx\n"), func(*syntax.Stmt) bool { return true }); err == nil {
		t.Error("Stmts err = nil, want the parser failure")
	}
}

// A panic in the caller's function is the caller's: Stmts reports only the
// parser's panics as errors. It used to turn the caller's bug into a
// "shell parser failed" error.
func TestStmts_CallerPanicGoesThrough(t *testing.T) {
	defer func() {
		if r := recover(); r != "caller bug" {
			t.Fatalf("recovered %v, want the caller's panic", r)
		}
	}()
	err := Stmts(syntax.NewParser(), strings.NewReader("a\n"), func(*syntax.Stmt) bool { panic("caller bug") })
	t.Fatalf("Stmts returned %v, want the caller's panic", err)
}

// A comment's trailing backslash is part of the comment, as in bash, not a
// line continuation: `echo a # note \` then `echo b` is two commands.
// mvdan's lexer joined the next line to the command before the comment,
// and ran `echo a echo b`; the editor's Alt+Enter after a comment writes
// exactly this. A backslash in quotes or a heredoc still continues or
// stays, and an escaped one (\\) ends the comment as it did.
func TestParse_CommentBackslashIsTheComments(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"echo a # note \\\necho b\n", "echo a\necho b"},
		{"echo a # one \\\necho b # two \\\necho c\n", "echo a\necho b\necho c"},
		{"f() { # c \\\n echo x; }\n", "f() {\n\techo x\n}"},
		{"echo a # n \\\\\necho b\n", "echo a\necho b"},
		{"echo \"x # y \\\nz\"\n", "echo \"x # y \\\nz\""},
		{"echo a \\\nb # c\n", "echo a \\\n\tb"},
		{"cat <<E\n# c \\\nd\nE\n", "cat <<E\n# c \\\nd\nE"},
	} {
		f, err := Parse(syntax.NewParser(), tc.src, "")
		if err != nil {
			t.Errorf("Parse(%q) error %v", tc.src, err)
			continue
		}
		var b strings.Builder
		if err := syntax.NewPrinter().Print(&b, f); err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(b.String()); got != tc.want {
			t.Errorf("Parse(%q) = %q, want %q", tc.src, got, tc.want)
		}
	}
}
