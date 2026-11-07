package completion

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/compat"
	"github.com/tfcace/hash/internal/parser"
	"github.com/tfcace/hash/internal/shlex"
)

// lineEnd is the quoting state a line leaves behind.
type lineEnd int

const (
	endCode    lineEnd = iota // plain code: a blank after it splits words
	endComment                // inside a comment that the newline ends
	endHeredoc                // code, with a heredoc body on the next lines
	endSubst                  // code inside an open $( )
	endQuote                  // inside an open quote
	endEscape                 // after a backslash: the next byte is literal
)

// The ?? marker search, the rc filter and completion read quoting through
// one lexer. For each tricky line they must agree on which bytes are quoted
// (the mask: . code, q quote, t quoted text, e escape, x escaped, #
// comment) and on what the line leaves open for the text after it:
//
//   - parser: line + " ?? q" asks the agent inline only from plain code;
//   - completion: in line + " x" the blank ends the word unless it is quoted
//     or escaped;
//   - filter: the next line of an rc file is a command of its own (and so
//     filtered) unless the line left a quote, backslash or heredoc open; in
//     an open $( ) it is a command too, of the substitution, which the
//     filter filters as well. closer is the line that ends that construct
//     in the rc file.
func TestQuotingAgreesAcrossConsumers(t *testing.T) {
	tests := []struct {
		line, mask string
		end        lineEnd
		closer     string
	}{
		{`echo "a??"`, `.....qtttq`, endCode, ""},
		{`echo 'it''s'`, `.....qttqqtq`, endCode, ""},
		{`echo $'a\'b'`, `......qtextq`, endCode, ""},
		{`x=$(echo "it's")`, `.........qttttq.`, endCode, ""},
		{`echo foo;# it's`, `.........######`, endComment, ""},
		{`(( a << b ))`, `............`, endCode, ""},
		{`make 2>&1`, `.........`, endCode, ""},
		{`cat <<EOF`, `.........`, endHeredoc, "EOF"},
		{`x=$(echo "it's"`, `.........qttttq`, endSubst, ")"},
		{`echo "it's`, `.....qtttt`, endQuote, `"`},
		{`echo 'a`, `.....qt`, endQuote, `'`},
		{`echo $'a\' b`, `......qtextt`, endQuote, `'`},
		{`echo a \`, `.......e`, endEscape, ""},
		{`echo trailing\`, `.............e`, endEscape, ""},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			if got := quotingMask(tt.line); got != tt.mask {
				t.Errorf("lexer mask\n got %s\nwant %s", got, tt.mask)
			}

			plain := tt.end == endCode || tt.end == endHeredoc
			marker := parser.Parse(tt.line + " ?? q")
			if isMarker := marker.Type == parser.CommandTypeAgentInline && marker.AgentPrompt == "q"; isMarker != plain {
				t.Errorf("parser: Parse(%q) = %+v, want an inline marker: %v", tt.line+" ?? q", marker, plain)
			}

			withArg := tt.line + " x"
			split := tt.end != endQuote && tt.end != endEscape
			if cw := parseCommandWords(withArg, len(withArg)); (cw.current == "x") != split {
				t.Errorf("completion: current word of %q = %q, want the blank to split it: %v", withArg, cw.current, split)
			}

			continues := tt.end >= endHeredoc && tt.end != endSubst
			if got := nextLineFiltered(t, tt.line, tt.closer); got == continues {
				t.Errorf("filter: line after %q filtered = %v, want it kept with the line: %v", tt.line, got, continues)
			}
		})
	}
}

func quotingMask(s string) string {
	var lex shlex.Lexer
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		b.WriteByte(".qtex#h"[lex.Step(s[i])])
	}
	return b.String()
}

// nextLineFiltered filters an rc file holding line, a zsh-only line, closer
// and a plain line, and reports whether the zsh-only line was neutralized.
func nextLineFiltered(t *testing.T, line, closer string) bool {
	t.Helper()
	content := line + "\nsetopt autocd\n"
	if closer != "" {
		content += closer + "\n"
	}
	rc := filepath.Join(t.TempDir(), ".zshrc")
	if err := os.WriteFile(rc, []byte(content+"export HASH_KEPT=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	filtered, _, err := compat.FilterWithDialect(rc, "zsh", "bash")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(filtered, "\n")[1] != "setopt autocd"
}
