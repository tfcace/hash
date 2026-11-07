package shlex

import (
	"strings"
	"testing"
)

// classMask lexes s and spells the class of each byte with one letter:
// . code, q quote, t quoted text, e escape, x escaped, # comment, h heredoc.
func classMask(s string) string {
	var l Lexer
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		b.WriteByte(".qtex#h"[l.Step(s[i])])
	}
	return b.String()
}

func TestLexer_Classes(t *testing.T) {
	tests := []struct{ in, want string }{
		{`echo "a??"`, `.....qtttq`},
		{`echo 'it''s'`, `.....qttqqtq`},
		{`$'a\'b' ??`, `.qtextq...`},
		{`"a\b\"c"`, `qtetextq`},
		{`a\ b`, `.ex.`},
		{`x=$(echo "it's") ??`, `.........qttttq....`},
		{"echo `a 'b'` c", "........qtq..."},
		{`echo "$(echo "it's")"`, `.....qt......qttttq.q`},
		{`echo foo;# it's`, `.........######`},
		{`echo a#b # c`, `.........###`},
		{`$(echo a)#b`, `...........`},
		{`a\ # b`, `.ex...`},
		{`(( a << b )) 'c'`, `.............qtq`},
		{`x=$(( 16#ff ))`, `..............`},
		{"(( a ))# it's", ".......######"},
		{"$((1))#x 'y'", ".........qtq"},
		{"cat <<EOF\nit's\nEOF\necho 'x'", "..........hhhhhhhhh.....qtq"},
		{"cat <<'E O'\n$x\nE O\n", "......qtttq.hhhhhhh"},
		{"cat <<-E\n\t'\n\tE\n'", ".........hhhhhhq"},
		{"cat <<E\n$(a 'b')c\nE\n", "........hh..qtq.hhhh"},
		{"cat <<<'it'", ".......qttq"},
		{"echo 1<<2 'x'", "..........qtq"},
		{"a \\\nb", "..ex."},
		{"a \\\n# it's", "..ex######"},
		{"echo `# it's` 'x'", "......######..qtq"},
		{`echo "unterminated`, `.....qtttttttttttt`},
		{`echo trailing\`, `.............e`},
		{`2>&1 'x'`, `.....qtq`},
		{"echo a >#b 'c", "........#####"},
		{"echo a<#b 'c", ".......#####"},
		{"cat <(echo a)#x 'y'", "................qtq"},
		{"tee >(cat)#x 'y'", ".............qtq"},
		{"cat < (echo a) #x", "...............##"},
		{"echo ${x:- #a} 'b'", "...............qtq"},
		{"echo ${x:-'a}'}", "..........qttq."},
		{`echo "${x:-"it's"}"`, `.....qt.tttqttttq.q`},
		{`echo "${x#"("}"`, `.....qt.ttqtq.q`},
		{`echo "${x:-a b;c}" d`, `.....qt.tttttttt.q..`},
	}
	for _, tt := range tests {
		if got := classMask(tt.in); got != tt.want {
			t.Errorf("classMask(%q)\n got %s\nwant %s", tt.in, got, tt.want)
		}
	}
}

func TestLexer_Continues(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"echo hi\n", false},
		{"echo 'open\n", true},
		{"echo \"$(open\n", true},
		{"x=$(( 1 +\n", true},
		{"echo a \\\n", true},
		{"echo a \\\nb\n", false},
		{"echo a \\\\\n", false},
		{"echo # it's \\\n", false},
		{"cat <<EOF\n", true},
		{"cat <<EOF\nbody\nEOF\n", false},
		{"cat <<A <<B\nA\n", true},
		{"cat <<A <<B\nA\nB\n", false},
		{"cat <<EOF \\\n  more\nEOF\n", false},
		{"cat <<-EOF\n\t\tEOF\n", false},
		{"cat <<E\\\nOF\nx\nEOF\n", false},
		{"cat <<F\nfoo \\\nF\n", true},
		{"cat <<F\nfoo \\\nF\nF\n", false},
		{"cat <<F\nfoo \\\\\nF\n", false},
		{"cat <<'F'\nfoo \\\nF\n", false},
		{"cat <<\\F\nfoo \\\nF\n", false},
		{"cat <<F\n\\\nF\n", false},
		{"cat <<E\\\nOF\nx\nE\nOF\n", true},
		{"cat <<\\\n EOF\nx\nEOF\n", false},
		{"cat <<\\\n EOF\nfoo \\\nEOF\n", true},
		{"cat <<''\\\nEOF\nfoo \\\nEOF\n", false},
		{"cat <<EOF\n\tEOF\n", true},
		{"x=$(cat <<EOF\nit's\nEOF\n)\n", false},
		{"x=`cat <<EOF\nfoo\nEOF`\n", false},
		{"x=`cat <<EOF\nfoo\n`\n", false},
		{"x=\"`cat <<EOF\nfoo\nEOF`\"\n", false},
		{"x=`cat <<EOF\nfoo \\`\n", true},
		{"cat <<'' \nbody\n\n", false},
		{"(( a << b ))\n", false},
		{"x=\"`echo \"it's\"`\"\n", false},
		{"x=$(case a in a) echo b;; esac\n", false}, // case patterns close $( ) early
		{"typeset -a dirs=(\n", true},
		{"dirs=(\n  ~/src\n)\n", false},
		{"(\n", true},
		{"( cd /tmp\n  ls )\n", false},
		{"f() { echo; }\n", false},
		{"x=$( (echo a) )\n", false},
		{"x=$( (echo a)\n", true},
		{"a=(1 2 # it's\n", true},
		{"( (( 1 ))\n", true},
		{"( (( 1 )) )\n", false},
		{"cat <((echo a) )\n", false},
		{"cat <((#a))\n", true},
		{"echo ${0#(}\n", false},
		{"echo ${x%(*}\n", false},
		{"echo ${x:-'a\n", true},
		{"echo ${x:-$(echo })}\n", false},
		{"echo \"${x:-\"it's\"}\"\n", false},
		{"echo \"${x#\"(\"}\"\n", false},
		{"echo \"${x:-'}'}\"\n", false},
		{"echo \"${x:-$(echo \")\")}\"\n", false},
		{"echo \"${x:-\"a\n", true},
		{"(( '1 ))\n", true},
		{"x=$(( '1' ))\n", false},
		{"(( `echo 1)) ))\n", true},
		{"x=$(( `echo 1` + 1 ))\n", false},
		// mvdan reads a $( ) in an unquoted body as code, lines and all: a
		// delimiter line inside it is a command.
		{"cat <<EOF\n$(echo\nEOF\n", true},
		{"cat <<EOF\n$(echo\nEOF\n)\nEOF\n", false},
		{"cat <<EOF\n$(cat <<X\nhi\nX\n)\nEOF\n", false},
		{"cat <<EOF\n$(cat <<X\nEOF\n", true},
		{"cat <<EOF\n${x:-'EOF\nEOF\n", true},
		{"cat <<EOF\n`echo\nEOF\n", true},
		{"cat <<EOF\n`echo`\nEOF\n", false},
		{"cat <<'EOF'\n$(echo\nEOF\n", false},
		{"cat <<E${x}\nE${x}\n", false},
	}
	for _, tt := range tests {
		var l Lexer
		l.Scan(tt.in)
		if got := l.Continues(); got != tt.want {
			t.Errorf("Continues after %q = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestLexer_TopLevelAndExpands(t *testing.T) {
	tests := []struct {
		in           string
		top, expands bool
		name         string
	}{
		{"echo ", true, true, "code"},
		{"echo $(", false, true, "substitution"},
		{`echo "`, false, true, "double quotes"},
		{`echo '`, false, false, "single quotes"},
		{`echo $'`, false, false, "ANSI-C quotes"},
		{`echo \`, true, false, "escape"},
		{`echo # `, true, false, "comment"},
		{`echo "$(`, false, true, "substitution in double quotes"},
		{"cat <<EOF\n", false, true, "heredoc body"},
		{"cat <<EOF\na\\", false, false, "escape in a heredoc body"},
		{"cat <<'EOF'\n", false, false, "quoted heredoc body"},
	}
	for _, tt := range tests {
		var l Lexer
		l.Scan(tt.in)
		if l.TopLevel() != tt.top || l.Expands() != tt.expands {
			t.Errorf("%s (%q): TopLevel = %v, Expands = %v; want %v, %v",
				tt.name, tt.in, l.TopLevel(), l.Expands(), tt.top, tt.expands)
		}
	}
}

func TestUnquote(t *testing.T) {
	tests := []struct{ in, want string }{
		{`My\ File`, "My File"},
		{`'My File`, "My File"},
		{`"My File`, "My File"},
		{`quote\'s.txt`, "quote's.txt"},
		{`double\"quote.txt`, `double"quote.txt`},
		{`back\\slash.txt`, `back\slash.txt`},
		{`"back\slash`, `back\slash`},
		{`"a\"b"`, `a"b`},
		{`$'it\'s'`, `it's`},
		{`$"x"`, "x"},
		{`$$'x'`, "$$x"},
		{`"a$"`, "a$"},
		{`\$'x'`, "$x"},
		{"a\\\nb", "ab"},
		{`trailing\`, `trailing\`},
		{`'it''s'`, "its"},
		{`$'a\tb'`, "a\tb"},
		{`$'a\nb\\c\"d\'e'`, "a\nb\\c\"d'e"},
		{`$'\x41\x4a2\101\0\e[0m'`, "AJ2A\x00\x1b[0m"},
		{`$'\a\b\f\r\v\E\?'`, "\a\b\f\r\v\x1b?"},
		{`$'\u00e9\U0001F600'`, "\u00e9\U0001F600"},
		{`$'\cA\q'`, "\x01\\q"},
		{`$'\x'`, `\x`},
		{`$'\c'`, `\c`},
		{`"\t"`, `\t`},
	}
	for _, tt := range tests {
		if got := Unquote(tt.in); got != tt.want {
			t.Errorf("Unquote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestWordStart(t *testing.T) {
	tests := []struct {
		line string
		pos  int
		want int
	}{
		{"cp file", 7, 3},
		{`cp My\ File`, 11, 3},
		{`cp 'My File`, 11, 3},
		{`cp "My File`, 11, 3},
		{`cp "${x:-a b`, 12, 3},
		{"cp file ", 8, 8},
		{"ls|gr", 5, 0},
		{`echo "$(git ch`, 14, 12},
		{"cp file", -1, 0},
		{"cp file", 99, 3},
		{"echo a\nfo", 9, 7},
		{"echo \"a\nb", 9, 5},
	}
	for _, tt := range tests {
		if got := WordStart(tt.line, tt.pos); got != tt.want {
			t.Errorf("WordStart(%q, %d) = %d, want %d", tt.line, tt.pos, got, tt.want)
		}
	}
}

// With GlobFlags (the rc filter reads zsh files), a ( right before # opens
// zsh glob flags, part of the word: (#i)xterm* and *(#qN) are words, not a
// group with a comment that never closes.
func TestLexer_GlobFlags(t *testing.T) {
	for _, tt := range []struct {
		in        string
		continues bool
	}{
		{"[[ $TERM == (#i)xterm* ]] && echo x\n", false},
		{"ls *(#qN) # it's\n", false},
		{"case $x in\n (#i)root) echo;;\nesac\n", false},
		{"[[ $x == (#b)(a)(b) ]]\n", false},
		{"( # it's a subshell\n", true},
		{"(#i\n", true},
	} {
		l := Lexer{GlobFlags: true}
		l.Scan(tt.in)
		if l.Continues() != tt.continues {
			t.Errorf("Continues after %q = %v, want %v", tt.in, l.Continues(), tt.continues)
		}
	}
	// bash reads (# as a subshell and a comment.
	var l Lexer
	l.Scan("[[ $TERM == (#i)xterm* ]] && echo x\n")
	if !l.Continues() {
		t.Error("without GlobFlags, (# should open a group and a comment")
	}
}

// A heredoc opened before a backtick or $( ) on the same line has its body
// after the line, as mvdan and bash read it; the newline inside the
// substitution starts only the bodies of heredocs opened inside it.
func TestLexer_HeredocAroundSubstitution(t *testing.T) {
	for _, tt := range []struct {
		in, mask string
	}{
		{"cat <<A; echo `cat <<B\nb\nB\n`\na\nA\nx",
			strings.Repeat(".", 23) + "hhhh..hhhh."},
		{"cat <<A; echo $(cat <<B\nb\nB\n)\na\nA\nx",
			strings.Repeat(".", 24) + "hhhh..hhhh."},
		{"cat <<A; echo $(cat <<B)\na\nA\nb\nB\nx", // mvdan reads both after the line
			strings.Repeat(".", 25) + "hhhhhhhh."},
	} {
		if got := classMask(tt.in); got != tt.mask {
			t.Errorf("classMask(%q)\n got %s\nwant %s", tt.in, got, tt.mask)
		}
	}
}
