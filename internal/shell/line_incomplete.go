package shell

import (
	"errors"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/tfcace/hash/internal/parser"
	"github.com/tfcace/hash/internal/shlex"
	"github.com/tfcace/hash/internal/shparse"
)

// lineIncomplete reports whether text, a line typed at the prompt, is
// shell syntax the executor's dialect reads as unfinished: an open
// compound command, heredoc, quote or pipe (the editor's Incomplete), or
// a line that ends with an escaped newline. It is read with the newline its
// last row ends with, as the shell reads a line, so `cat <<EOF` reads on.
// An agent request's prompt is prose, never unfinished.
func (s *Shell) lineIncomplete(text string) bool {
	if parser.Parse(text).Type != parser.CommandTypeRegular {
		return false
	}
	var lang syntax.LangVariant
	if err := lang.Set(s.executor.Dialect()); err != nil {
		lang = syntax.LangBash
	}
	line := text + "\n"
	_, err := shparse.Parse(syntax.NewParser(syntax.Variant(lang)), line, "")
	if err != nil {
		return syntax.IsIncomplete(err) || unclosedHeredoc(err)
	}
	// The parser takes an escaped newline at the end of its input as the
	// end; the shell reads the next line onto it. That is all the lexer is
	// asked: the parser read the line, and the lexer's own reading of it
	// (bash's (# as a group and a comment) never overrides that.
	lex := shlex.Lexer{GlobFlags: lang == syntax.LangZsh}
	lex.Scan(line)
	return lex.Continued()
}

// unclosedHeredoc reports whether err is mvdan's here-document that reached
// the end of the input before its delimiter line. That is unfinished
// whatever the delimiter's quoting, but mvdan marks it Incomplete only for
// an unquoted one: a quoted delimiter's body is read as raw bytes, with no
// open word for its Incomplete to see. Without this, Enter on `cat <<'EOF'`
// ran the line and each body line after it ran as a command.
func unclosedHeredoc(err error) bool {
	var perr syntax.ParseError
	return errors.As(err, &perr) && strings.HasPrefix(perr.Text, "unclosed here-document")
}
