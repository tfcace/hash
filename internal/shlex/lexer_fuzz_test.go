package shlex

import (
	"regexp"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

// FuzzLexer checks the lexer against mvdan's bash parser, which knows the
// quoting rules from the other side: a program mvdan parses whole must not
// leave the lexer continuing (the rc filter would swallow the lines after
// it), and one mvdan finds cut short in a quote, substitution, group or
// heredoc must. It also checks that the classes are well formed.
//
// Some inputs are left out on purpose, where the lexer deviates from bash by
// design or mvdan from both:
//   - heredoc delimiters that start with a digit, ! or other punctuation:
//     the lexer takes 1<<2 and $x<<y for shifts, not heredocs;
//   - extglob patterns such as @(a|b) or !(x): mvdan reads them as one
//     word, the lexer as a group of code;
//   - backslashes inside backticks, whose second level of escaping the
//     lexer does not model;
//   - a # glued to the word before it inside ( ), $( ) or backticks
//     ($(echo ""#x), ($0#)), or inside a heredoc delimiter (<<"E"#x):
//     mvdan starts a comment there, bash (and the lexer) go on with the word;
//   - a backslash inside double quotes in a heredoc delimiter (<<"\"x"):
//     bash (and the lexer) drop it as quote removal does, mvdan keeps it, so
//     its delimiter line differs;
//   - bash 5.3's ${ cmd; } and ${| cmd; }, which mvdan reads as commands and
//     the lexer as a parameter expansion;
//   - a quote right after ${ or a name's [: mvdan takes ${"x} for a
//     parameter named "x and A["="]" for an assignment, bash for a quote
//     still open, as the lexer does;
//   - a group opened on a heredoc's line after the << (<<A; (echo): bash
//     (and the lexer) read the body after that line, mvdan only after the
//     newline that ends the statement around the group;
//   - a backslash-newline in the body of an unquoted heredoc: bash (and the
//     lexer) join the lines before looking for the delimiter, mvdan does not;
//   - some expansions in a heredoc body: mvdan reads a body line that
//     goes on after an expansion ($0EOF, $()EOF, `x`EOF) as the delimiter,
//     a delimiter with $ or ` in it never ends, heredocs started in a
//     substitution in a body get their bodies in another order, and a body
//     inside backticks ends at a backtick for bash (and the lexer) only;
//   - control and non-ASCII bytes, which mvdan rejects or decodes as UTF-8.
//
// With GlobFlags, zsh glob flags (#...) are part of a word: the lexer must
// read an input as it reads it with each simple (#...) replaced by a letter
// without GlobFlags (no heredocs: a letter starts a delimiter, ( does not).
// (mvdan's zsh parser is no reference here: it accepts
// an unclosed ${ and reads $(# without a comment, where zsh starts one.)
//
// A backslash-newline at the very end leaves the lexer continuing although
// mvdan, at the end of input, ignores it: read a line at a time, as the rc
// filter does, it joins the next line.
//
// Shell keywords, braces, brackets and trailing operators make an input
// mvdan calls incomplete without opening anything the lexer tracks (if
// without fi, [[ without ]], a[ read as an array element, a && at the
// end), so the second check skips inputs with them.
func FuzzLexer(f *testing.F) {
	for _, seed := range []string{
		`echo "a??"`,
		`echo 'it''s'`,
		`$'a\'b' ?? q`,
		`x=$(echo "it's") ?? q`,
		`echo foo;# it's`,
		"(( a << b ))",
		"cat <<EOF\nit's\nEOF\n",
		"cat <<-'E O'\n\tE O\n",
		"cat <<E\\\nOF\nx\nEOF\n",
		"echo a \\\nb",
		"make 2>&1 | ?? q",
		`echo "unterminated`,
		`echo trailing\`,
		"x=\"`echo \"$(echo \"it's\")\"`\"",
		"x=$(( $(echo 1) + 16#ff ))",
		"typeset -a dirs=(\n  ~/src\n)\n",
		"a=(1 2 # c\n3)\n",
		"( cd /tmp\n  ls )\n",
		"x=$(case a in (a) echo;; esac)\n",
		"echo ${x#'a'}\n",
		"echo $'a\\'b'\n",
		"echo \"${x:-\"it's\"}\"\n",
		"echo \"${x#\"(\"}\" | ?? q\n",
		"echo \"${x:-'}'}\"\n",
		"[[ $TERM == (#i)xterm* ]] && echo 'x'\n",
		"ls *(#qN) # it's\n",
		"cat <<A; echo `cat <<B\nb\nB\n`\na\nA\n",
		"cat <<A; echo $(cat <<B\nb\nB\n)\na\nA\n",
		"cat <<A; x=$(cat <<B)\na\nA\nb\nB\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		var l Lexer
		classes := make([]Class, len(s))
		for i := 0; i < len(s); i++ {
			classes[i] = l.Step(s[i])
		}
		checkClasses(t, s, classes)
		checkGlobFlags(t, s)
		if !l.Continues() && !l.TopLevel() {
			t.Fatalf("%q: complete but nested", s)
		}
		_ = Unquote(s)
		if start := WordStart(s, len(s)/2); start < 0 || start > len(s)/2 {
			t.Fatalf("WordStart(%q, %d) = %d", s, len(s)/2, start)
		}

		closed := s + l.Closer()
		// nothing joins the closer, and each delimiter is a plain word (a
		// line cannot match a newline, a tab <<- strips or a lone backslash)
		// (and no heredoc starts in a body, whose bodies mvdan orders its own
		// way)
		closes := l.mode == modeNormal && l.delim.phase == delimNone && !l.continued && plainDelims(s) &&
			!bodyHeredoc.MatchString(s)
		if closes {
			var c Lexer
			c.Scan(closed)
			if c.Continues() {
				t.Fatalf("%q with its Closer %q still continues", s, l.Closer())
			}
		}

		if !comparable(s) {
			return
		}
		program := s + "\n"
		l = Lexer{}
		l.Scan(program)
		_, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(program), "")
		if err == nil || syntax.IsIncomplete(err) {
			if closes && !opaque(s) && !opaque(closed) && !oddEnd(s) {
				// what the lexer does not track (an if without fi) is left to the
				// parser's recovery, and other errors (an empty group) are not
				// the closer's: it must close quotes, substitutions and bodies
				_, err := syntax.NewParser(syntax.RecoverErrors(8)).Parse(strings.NewReader(closed), "")
				if err != nil && (strings.Contains(err.Error(), "reached EOF without") || strings.Contains(err.Error(), "unclosed here-document")) {
					t.Fatalf("mvdan rejects %q, closed by the lexer: %v", closed, err)
				}
			}
		}
		switch {
		case err == nil && l.Continues() && !l.continued:
			t.Fatalf("mvdan parses %q, but the lexer says it continues", program)
		case syntax.IsIncomplete(err) && !opaque(s) && !l.Continues():
			t.Fatalf("mvdan says %q is cut short (%v), but the lexer says it is complete", program, err)
		}
	})
}

func checkGlobFlags(t *testing.T, s string) {
	t.Helper()
	plain := globFlags.ReplaceAllString(s, "x")
	if plain == s || strings.Contains(plain, "(#") || notGlobFlags.MatchString(s) || strings.Contains(s, "<<") {
		return
	}
	zsh := Lexer{GlobFlags: true}
	zsh.Scan(s + "\n")
	var bash Lexer
	bash.Scan(plain + "\n")
	if zsh.Continues() != bash.Continues() {
		t.Fatalf("%q: with GlobFlags Continues = %v, but %v for %q", s, zsh.Continues(), bash.Continues(), plain)
	}
}

func checkClasses(t *testing.T, s string, classes []Class) {
	t.Helper()
	for i, c := range classes {
		if c > Heredoc {
			t.Fatalf("%q: byte %d has class %d", s, i, c)
		}
		if i > 0 && classes[i-1] == Escape && c != Escaped && c != Quoted {
			t.Fatalf("%q: byte %d after an escape is class %d", s, i, c)
		}
	}
}

var (
	gluedHash   = regexp.MustCompile("[(`](?s:.*)[^ \t\n;|&(<>]#")
	bodyExpand  = regexp.MustCompile("<<[^\n]*\n(?s:.*)([)`}]|\\$[\\w!@#?*$-]*)\\w|<<-?[ \t]*[^ \t\n]*[$`]|`[^`]*<<|<<[^\n]*\n(?s:.*)[$`][^\n]*<<")
	joinedBody  = regexp.MustCompile(`<<[^\n]*\n(.*\n)*.*\\\n`)
	oddHeredoc  = regexp.MustCompile(`<<-?([ \t]|\\\n)*([^a-zA-Z_'"\\< \t\n]|[^ \t\n]*#)`)
	extglob     = regexp.MustCompile(`[?*+@!]\(`)
	quotedName  = regexp.MustCompile(`(\$\{|\w\[)['"]|\$\{[ \t\n|]`)
	groupAfter  = regexp.MustCompile(`<<[^\n]*[^$<>\n]\(`)
	bodyHeredoc = regexp.MustCompile(`<<[^\n]*\n(?s:.*)<<`)
	plainDelim  = regexp.MustCompile(`^<<-?[ \t]*(['"]?)[A-Za-z_][A-Za-z0-9_]*(['"]?)([ \t\n;|&)<>]|$)`)
	globFlags   = regexp.MustCompile(`\(#[a-zA-Z0-9.,:+-]*\)`) // (#i), (#a2), (#qN)
	// a (# that opens a substitution, arithmetic or an escaped paren
	notGlobFlags = regexp.MustCompile(`[$<>(\\]\(#`)
	quotedEsc    = regexp.MustCompile(`<<-?[ \t]*[^ \t\n]*"[^"\n]*\\`)
	keyword      = regexp.MustCompile(`(^|[^a-zA-Z0-9_])(if|then|else|elif|fi|do|done|while|until|for|select|case|esac|in|function|time|coproc)([^a-zA-Z0-9_]|$)|[[{}!]`)
)

// comparable reports whether s is in the part of the language where the
// lexer means to agree with bash (see FuzzLexer).
func comparable(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 && s[i] != '\n' && s[i] != '\t' || s[i] >= 0x7f {
			return false
		}
	}
	return !oddHeredoc.MatchString(s) && !extglob.MatchString(s) && !quotedEsc.MatchString(s) && !groupAfter.MatchString(s) && !quotedName.MatchString(s) && !gluedHash.MatchString(s) &&
		!joinedBody.MatchString(s+"\n") && !bodyExpand.MatchString(s) &&
		(!strings.Contains(s, "`") || !strings.Contains(s, `\`))
}

// plainDelims reports whether every << in s (but a <<< here-string) starts
// a heredoc with a plain word, maybe quoted, for its delimiter.
func plainDelims(s string) bool {
	for i := 0; ; {
		next := strings.Index(s[i:], "<<")
		if next < 0 {
			return true
		}
		i += next
		if strings.HasPrefix(s[i:], "<<<") {
			i += 3
			continue
		}
		if m := plainDelim.FindStringSubmatch(s[i:]); m == nil || m[1] != m[2] {
			return false
		}
		i += 2
	}
}

// oddEnd reports whether s ends where a closing quote or paren would not
// parse: after a heredoc operator or a $ that the closer would complete.
func oddEnd(s string) bool {
	return strings.HasSuffix(s, "<") || strings.HasSuffix(s, "$") || strings.HasSuffix(s, ">")
}

// opaque reports whether s may be incomplete for a reason the lexer does not
// track: a keyword or brace still open, or an operator, a redirection or a
// function's () at the end.
func opaque(s string) bool {
	if keyword.MatchString(s) {
		return true
	}
	var l Lexer
	var prev, last byte // the last two code bytes, not counting blanks, comments,
	// heredoc bodies and line continuations
	for i := 0; i < len(s); i++ {
		class := l.Step(s[i])
		if class == Comment || class == Heredoc || class == Escape || class == Escaped && s[i] == '\n' ||
			class == Code && (isBlank(s[i]) || s[i] == '\n') {
			continue
		}
		prev, last = last, s[i]
		if class != Code {
			last = 'w' // quoted or escaped: part of a word
		}
	}
	return strings.IndexByte("|&;<>", last) >= 0 || prev == '(' && last == ')'
}
