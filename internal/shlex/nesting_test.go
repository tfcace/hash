package shlex

import (
	"strings"
	"testing"
)

// nestingMask lexes s and spells, for each byte, the substitution depth
// before it, or 'a' when the byte is inside an expression (arithmetic or
// ${ }).
func nestingMask(s string) string {
	var l Lexer
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if l.InExpression() {
			b.WriteByte('a')
		} else {
			b.WriteByte(byte('0' + l.Substitutions()))
		}
		l.Step(s[i])
	}
	return b.String()
}

func TestLexer_SubstitutionsAndExpressions(t *testing.T) {
	tests := []struct{ in, want string }{
		{"ls $(pwd)/x", "00000111100"},
		{"a `b` c", "0001100"},
		{`"$(a)"`, "000110"},
		{"$(a $(b))c", "0011112210"},
		{"$(a (b) c)d", "00111111110"},
		{"$((1 + 2))x", "001aaaaaaa0"},
		{"(( a < b )) c", "00aaaaaaaaa00"},
		{"$(echo $((1)))", "0011111112aaa1"},
		{"'$(a'", "00000"},
		{"${x:-a b}c", "00aaaaaaa0"},
		{"${x:-$(a b)}c", "00aaaaa1111a0"},
		{`${x:-"a b"}c`, "00aaaa0000a0"},
		// An unquoted heredoc body expands $( ), backticks and ${ }: code
		// again inside them. A quoted one is text.
		{"cat <<E\n$(a)b`c`", "0000000000110011"},
		{"cat <<E\n${a b}", "0000000000aaaa"},
		{"cat <<'E'\n$(a)b", "000000000000000"},
	}
	for _, tt := range tests {
		if got := nestingMask(tt.in); got != tt.want {
			t.Errorf("nestingMask(%q)\n got %s\nwant %s", tt.in, got, tt.want)
		}
	}
}

func TestLexer_GluesRedirection(t *testing.T) {
	tests := []struct {
		before string
		c      byte
		want   bool
	}{
		{"ls 2>", '&', true},
		{"ls <", '&', true},
		{"ls >", '|', true},
		{"ls <", '|', false},
		{"ls ", '&', false},
		{"ls ", '|', false},
		{"ls >x", '&', false},
		{"echo '>", '&', false},
		{"echo $((1 >", '&', false},
	}
	for _, tt := range tests {
		var l Lexer
		l.Scan(tt.before)
		if got := l.GluesRedirection(tt.c); got != tt.want {
			t.Errorf("after %q, GluesRedirection(%q) = %v, want %v", tt.before, tt.c, got, tt.want)
		}
	}
}

func TestLexer_ContextText(t *testing.T) {
	eof, literal := Body{Delim: "EOF"}, Body{Delim: "EOF", Literal: true}
	tests := []struct {
		in   string
		want Text
	}{
		{"cat <<EOF", nil},
		{"cat <<EOF\n", eof},
		{"cat <<EOF\nfoo \\", eof},
		{"cat <<EOF\nfoo\nEOF", eof},
		{"cat <<EOF\nfoo\nEOF\n", nil},
		{"cat <<EOF; echo", nil},
		{"echo # a", CommentText{}},
		{"echo # a\n", nil},
		{"echo a#b", nil},
		{"echo `ls # a", CommentText{}},
		{"echo `ls # a`", nil},
		{"cat <<EOF\n$(ls ", nil},
		{"cat <<EOF\n$(ls\n", nil},
		{"cat <<EOF\n`ls ", nil},
		{"cat <<EOF\n${x:-", nil},
		{"cat <<EOF\n$(ls) ", eof},
		{"cat <<EOF\n$(ls # a", CommentText{}},
		{"cat <<'EOF'\n", literal},
		{"cat <<'EOF'\n$(ls ", literal},
		{"cat <<E\"OF\"\n", literal},
		{"cat <<EOF <<'X'\nEOF\n", Body{Delim: "X", Literal: true}},
		{"cat <<'X' <<EOF\nX\n", eof},
		{"cat <<-'E O'\n\t", Body{Delim: "E O", StripTabs: true, Literal: true}},
		{"cat <<A <<-B\nA\n", Body{Delim: "B", StripTabs: true}},
		{"cat <<A\n$(cat <<B\nb", Body{Delim: "B"}},
		{"cat <<A\n$(cat <<B\nb\nB\n)", Body{Delim: "A"}},
	}
	for _, tt := range tests {
		var l Lexer
		l.Scan(tt.in)
		if got := l.Context().Text; got != tt.want {
			t.Errorf("after %q, Context().Text = %#v, want %#v", tt.in, got, tt.want)
		}
	}
}

func TestLexer_ContextEscaped(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"echo a\\", true},
		{"echo a\\\\", false},
		{`echo "a\`, true},
		{`echo $'a\`, true},
		{"cat <<EOF\na\\", true},
		{"cat <<EOF\na\\\n", false},
		{"cat <<'EOF'\na\\", false},
		{"echo # a\\", false},
	}
	for _, tt := range tests {
		var l Lexer
		l.Scan(tt.in)
		if got := l.Context().Escaped; got != tt.want {
			t.Errorf("after %q, Context().Escaped = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestLexer_ContextQuote(t *testing.T) {
	tests := []struct {
		in   string
		want QuoteKind
	}{
		{"echo ", NoQuote},
		{"echo 'a", SingleQuote},
		{`echo "a`, DoubleQuote},
		{`echo $'a\'`, ANSIQuote},
		{`echo "a" b`, NoQuote},
		{`echo "$(ls `, NoQuote},
		{`echo "$(ls "`, DoubleQuote},
		{`echo "a\`, DoubleQuote},
		{"echo # '", NoQuote},
		{"cat <<EOF\n'", NoQuote},
		{"cat <<EOF\n$(ls '", SingleQuote},
	}
	for _, tt := range tests {
		var l Lexer
		l.Scan(tt.in)
		if got := l.Context().Quote; got != tt.want {
			t.Errorf("Context().Quote after %q = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestLexer_ContextDepth(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"echo ", 0},
		{`echo "a`, 1},
		{"echo `ls \"a", 2},
		{"echo `ls \"a`", 3}, // a backtick inside the quote opens one
		{"echo `ls \"a\"", 1},
		{"cat <<EOF\n$(", 2},
	}
	for _, tt := range tests {
		var l Lexer
		l.Scan(tt.in)
		if got := l.Context().Depth; got != tt.want {
			t.Errorf("Context().Depth after %q = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestLexer_ContextParam(t *testing.T) {
	tests := []struct {
		in   string
		want ParamKind
	}{
		{"echo ", NoParam},
		{"echo ${x:-", SplitParam},
		{`echo "${x:-`, QuotedParam},
		{`echo "${x:-${y:-`, QuotedParam},
		{"cat <<EOF\n${x:-", QuotedParam},
		{`echo ${x:-"`, NoParam},
		{"echo ${x:-$(", NoParam},
	}
	for _, tt := range tests {
		var l Lexer
		l.Scan(tt.in)
		if got := l.Context().Param; got != tt.want {
			t.Errorf("Context().Param after %q = %d, want %d", tt.in, got, tt.want)
		}
	}
}

// Closer closes what a line leaves open, innermost first: quotes,
// substitutions, ${ }, then the heredoc bodies still to read, with their
// delimiters, so that the line parses whole.
func TestLexer_Closer(t *testing.T) {
	tests := []struct{ in, want string }{
		{"echo hi", ""},
		{`echo "a`, `"`},
		{"echo $(ls '", "')"},
		{"echo `ls ${x:-", "}`"},
		{"echo $(ls # a", " \n)"},
		{"cat <<EOF", "\nEOF\n"},
		{"cat <<EOF\n", "EOF\n"},
		{"cat <<EOF\nab", "\nEOF\n"},
		{"cat <<A <<'B'\nab", "\nA\nB\n"},
		{"cat <<EOF\n$(echo ", ")\nEOF\n"},
		{"cat <<-EOF\n\t", "EOF\n"},
		{"echo $(cat <<A", ")\nA\n"},
		{"cat <<A; echo $(cat <<B", ")\nA\nB\n"},
		{"cat <<A\n$(cat <<B", ")\nA\nB\n"}, // B joins A's bodies, as the lexer reads it
		// a pending escape would take the closer's first byte: an escaped
		// blank goes first
		{`echo "a\`, ` "`},
		{`echo a\`, ` `},
		{"echo $(ls a\\", " )"},
		{"cat <<EO\\", " \nEO \n"},
	}
	for _, tt := range tests {
		var l Lexer
		l.Scan(tt.in)
		if got := l.Closer(); got != tt.want {
			t.Errorf("after %q, Closer() = %q, want %q", tt.in, got, tt.want)
		}
		l.Scan(tt.want)
		if l.Continues() {
			t.Errorf("%q closed with %q still continues", tt.in, tt.want)
		}
	}
}
