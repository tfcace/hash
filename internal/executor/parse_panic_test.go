package executor

import (
	"context"
	"io"
	"testing"
)

// mvdan's zsh parser panics on some heredoc words; a command line with one is
// a syntax error, not a crash of the shell.
func TestExecute_ParserPanicIsASyntaxError(t *testing.T) {
	e := New()
	if err := e.SetDialect("zsh"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(context.Background(), "cat <<$a[\nx\n", io.Discard, io.Discard); err == nil {
		t.Error("Execute = nil error, want a parse error")
	}
	if _, ok := e.SimpleCommandWords("cat <<$a[\nx\n"); ok {
		t.Error("SimpleCommandWords ok, want not simple")
	}
}
