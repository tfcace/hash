// internal/compat/tokens.go
package compat

import (
	"slices"
	"strings"

	"github.com/tfcace/hash/internal/shlex"
)

// skipComment marks what the filter rewrote: the original text follows it.
const skipComment = "# [hash-compat] "

// squeeze returns tok without its blanks: f( ) is f().
func squeeze(tok string) string {
	return strings.Join(strings.Fields(tok), "")
}

func isOperator(tok string) bool {
	return strings.Trim(tok, ";&|") == ""
}

// codeTokens splits stmt into its raw words and control operators (; & &&
// || | |& ;;), split at blanks and newlines outside quotes, substitutions and
// groups, leaving out comments. A quoted keyword ('fi') keeps its quotes, so
// it never matches the keyword, and a $( ) or ( ) stays inside its word.
func codeTokens(stmt string) []string {
	l := shlex.Lexer{GlobFlags: true}
	var toks []string
	start, op := 0, false // the token in progress starts at start; op: it is an operator
	flush := func(i int) {
		tok := stmt[start:i]
		if strings.HasSuffix(squeeze(tok), "(){") { // f(){ opens the body
			toks = append(toks, tok[:len(tok)-1])
			tok = "{"
		}
		if tok != "" {
			toks = append(toks, tok)
		}
		start = i
	}
	for i := 0; i < len(stmt); i++ {
		top := l.TopLevel()
		class := l.Step(stmt[i])
		c := stmt[i]
		switch {
		case class == shlex.Comment || class == shlex.Heredoc:
			flush(i)
			start = i + 1
		case !top || class != shlex.Code:
			if op {
				flush(i)
				op = false
			}
		case c == ' ' || c == '\t' || c == '\n':
			flush(i)
			start, op = i+1, false
		case strings.IndexByte(";&|", c) >= 0:
			if !op {
				flush(i)
				op = true
			}
		case op:
			flush(i)
			op = false
		}
	}
	flush(len(stmt))
	return toks
}

// codeWords returns the words of line, without its control operators.
func codeWords(line string) []string {
	return slices.DeleteFunc(codeTokens(line), isOperator)
}

// closers are the words that end or continue a compound command the line
// they start does not open.
var closers = []string{"}", "fi", "done", "esac", "then", "else", "elif", "do", ";;", ";&", ";;&", ";|"}

// startsWithCloser reports whether code starts with a closer.
func startsWithCloser(code string) bool {
	line, _, _ := strings.Cut(strings.TrimLeft(code, " \t\n;"), "\n")
	toks := codeTokens(line)
	return len(toks) > 0 && slices.Contains(closers, toks[0])
}
