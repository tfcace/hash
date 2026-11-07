package completion

import (
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/shlex"
)

func TestEscapeShellWord(t *testing.T) {
	tests := []struct {
		name string
		word string
		want string
	}{
		{name: "safe", word: "src/main.go", want: "src/main.go"},
		{name: "space", word: "My File.txt", want: `My\ File.txt`},
		{name: "single quote", word: "quote's.txt", want: `quote\'s.txt`},
		{name: "double quote", word: `double"quote.txt`, want: `double\"quote.txt`},
		{name: "backslash", word: `back\slash.txt`, want: `back\\slash.txt`},
		{name: "semicolon", word: "semi;colon.txt", want: `semi\;colon.txt`},
		{name: "dollar", word: "price$1.txt", want: `price\$1.txt`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EscapeShellWord(tt.word); got != tt.want {
				t.Fatalf("EscapeShellWord() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseCommandWords(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		pos     int // -1 = end of line
		words   []string
		current string
		start   int
	}{
		{"empty", "", -1, nil, "", 0},
		{"command being typed", "gi", -1, nil, "gi", 0},
		{"after command", "git ", -1, []string{"git"}, "", 4},
		{"argument", "ls -la", -1, []string{"ls"}, "-la", 3},
		{"single pipe", "cat file | grep", -1, nil, "grep", 11},
		{"pipe with spaces", "cat file |   gr", -1, nil, "gr", 13},
		{"multiple pipes", "cat file | grep foo | wc", -1, nil, "wc", 22},
		{"cursor before pipe", "cat f | grep", 5, []string{"cat"}, "f", 4},
		{"empty after pipe", "cat file | ", -1, nil, "", 11},
		{"pipe glued to word", "ls|gr", -1, nil, "gr", 3},
		{"or", "a || b c", -1, []string{"b"}, "c", 7},
		{"and", "make && ./run x", -1, []string{"./run"}, "x", 14},
		{"semicolon", "cd /tmp; ls fo", -1, []string{"ls"}, "fo", 12},
		{"background", "sleep 1 & jobs", -1, nil, "jobs", 10},
		{"command substitution", "echo $(git log ma", -1, []string{"git", "log"}, "ma", 15},
		{"backticks", "echo `whi", -1, nil, "whi", 6},
		{"subshell", "(cd x && ls y", -1, []string{"ls"}, "y", 12},
		{"single quotes keep spaces", "cat 'my fi", -1, []string{"cat"}, "my fi", 4},
		{"double quotes keep spaces", `cat "my fi`, -1, []string{"cat"}, "my fi", 4},
		{"escaped space", `cat my\ fi`, -1, []string{"cat"}, "my fi", 4},
		{"quoted operators are literal", `git log --grep="a|b;c" ma`, -1, []string{"git", "log", "--grep=a|b;c"}, "ma", 23},
		{"escaped operator is literal", `echo a\|b c`, -1, []string{"echo", "a|b"}, "c", 10},
		{"quoted empty word", `git checkout ''`, -1, []string{"git", "checkout"}, "", 13},
		{"fd duplication is not an argument", "make -k 2>&1 tar", -1, []string{"make", "-k"}, "tar", 13},
		{"stdout to stderr is not an argument", "echo hi >&2 x", -1, []string{"echo", "hi"}, "x", 12},
		{"input duplication is not an argument", "cat <&0 f", -1, []string{"cat"}, "f", 8},
		{"both streams redirect is not an argument", "make &>log tar", -1, []string{"make"}, "tar", 11},
		{"redirect target is not an argument", "git checkout > out ma", -1, []string{"git", "checkout"}, "ma", 19},
		{"quoted > is an argument", "echo '>' x", -1, []string{"echo", ">"}, "x", 9},
		{"assignment before command", "FOO=1 git checkout ma", -1, []string{"git", "checkout"}, "ma", 19},
		{"command after assignments", "A=1 B=2 gi", -1, nil, "gi", 8},
		{"assignment-looking argument", "make CC=gcc ta", -1, []string{"make", "CC=gcc"}, "ta", 12},
		{"quoted assignment is a command", "'A=1' x", -1, []string{"A=1"}, "x", 6},
		{"fd duplication being typed completes its target", "ls 2>&1", -1, []string{"ls"}, "1", 6},
		{"escaped > does not start a redirection", `echo \>& jobs`, -1, nil, "jobs", 9},
		{"pipe both streams is an operator", "make |& gr", -1, nil, "gr", 8},
		{"clobber redirection is not a pipe", "echo hi >| ou", -1, []string{"echo", "hi"}, "ou", 11},
		{"clobber target is not an argument", "echo hi >| out x", -1, []string{"echo", "hi"}, "x", 15},
		{"glued clobber redirection", "echo hi 2>|ou", -1, []string{"echo", "hi"}, "ou", 11},
		{"pipe after a redirection target", "echo hi >out | gr", -1, nil, "gr", 15},
		{"redirection glued after a word", "echo hi>ou", -1, []string{"echo", "hi"}, "ou", 8},
		{"argument after a redirection glued after a word", "echo hi>out x", -1, []string{"echo", "hi"}, "x", 12},
		{"input redirection glued after a word", "sort data<in x", -1, []string{"sort", "data"}, "x", 13},
		{"fd redirection is one word", "make 2>log x", -1, []string{"make"}, "x", 11},
		{"here-string glued", "cat <<<ou", -1, []string{"cat"}, "ou", 7},
		{"stderr to stdout is one word", "make 2>&1 x", -1, []string{"make"}, "x", 10},
		{"both streams glued after a word", "make all&>log x", -1, []string{"make", "all"}, "x", 14},
		{"append glued after a word", "echo hi>>ou", -1, []string{"echo", "hi"}, "ou", 9},
		{"substitution in double quotes", `echo "$(git ch`, -1, []string{"git"}, "ch", 12},
		{"backticks in double quotes", "echo \"`whi", -1, nil, "whi", 7},
		{"quotes inside a substitution", `echo $(echo "a b" c`, -1, []string{"echo", "a b"}, "c", 18},
		{"ANSI-C quote escapes its quote", `cat $'it\'s fi`, -1, []string{"cat"}, "it's fi", 4},
		{"word after a substitution is an argument", "echo $(date) fo", -1, []string{"echo", "$(date)"}, "fo", 13},
		{"word after backticks is an argument", "echo `date` fo", -1, []string{"echo", "`date`"}, "fo", 12},
		{"word after arithmetic is an argument", "echo $((1 + 2)) fo", -1, []string{"echo", "$((1 + 2))"}, "fo", 16},
		{"substitution inside the current word", "ls $(pwd)/", -1, []string{"ls"}, "$(pwd)/", 3},
		{"substitution in double quotes then an argument", `echo "$(date)" fo`, -1, []string{"echo", "$(date)"}, "fo", 15},
		{"nested substitutions", "echo $(dirname $(pwd)) fo", -1, []string{"echo", "$(dirname $(pwd))"}, "fo", 23},
		{"inside a substitution after a nested one", "echo $(dirname $(pwd) x", -1, []string{"dirname", "$(pwd)"}, "x", 22},
		{"assignment from a substitution", "X=$(pwd) gi", -1, nil, "gi", 9},
		{"process substitution is an argument", "diff <(ls) fo", -1, []string{"diff", "<(ls)"}, "fo", 11},
		{"inside a process substitution", "diff <(ls -", -1, []string{"ls"}, "-", 10},
		{"subshell inside a process substitution", "cat <( (ls); gi", -1, nil, "gi", 13},
		{"after a process substitution with a subshell", "cat <( (ls) ) fo", -1, []string{"cat", "<( (ls) )"}, "fo", 14},
		{"output process substitution", "make >(tee log) -k", -1, []string{"make", ">(tee log)"}, "-k", 16},
		{"arithmetic command", "(( i++ )) && gi", -1, nil, "gi", 13},
		{"after then", "if true; then gi", -1, nil, "gi", 14},
		{"after if", "if gi", -1, nil, "gi", 3},
		{"after do", "for f in *; do ec", -1, nil, "ec", 15},
		{"after while", "while gi", -1, nil, "gi", 6},
		{"after a brace group opens", "{ gi", -1, nil, "gi", 2},
		{"after negation", "! git checkout ma", -1, []string{"git", "checkout"}, "ma", 15},
		{"after time", "time git checkout ma", -1, []string{"git", "checkout"}, "ma", 18},
		{"after several keywords", "if ! time gi", -1, nil, "gi", 10},
		{"after else", "if a; then b; else gi", -1, nil, "gi", 19},
		{"keyword as an argument", "echo then x", -1, []string{"echo", "then"}, "x", 10},
		{"quoted keyword is a command", "'then' x", -1, []string{"then"}, "x", 7},
		{"blank inside a parameter expansion", "echo ${x:-a b} gi", -1, []string{"echo", "${x:-a b}"}, "gi", 15},
		{"pattern blank inside a parameter expansion", "git checkout ${x// /_} ma", -1, []string{"git", "checkout", "${x// /_}"}, "ma", 23},
		{"paren inside a parameter expansion", "git checkout ${x:-(} ma", -1, []string{"git", "checkout", "${x:-(}"}, "ma", 21},
		{"pipe inside a parameter expansion", "git checkout ${x:-a|b} ma", -1, []string{"git", "checkout", "${x:-a|b}"}, "ma", 23},
		{"semicolon inside a parameter expansion", "git checkout ${x:-a;b} ma", -1, []string{"git", "checkout", "${x:-a;b}"}, "ma", 23},
		{"close paren inside a parameter expansion", "echo ${x:-a)b} ", -1, []string{"echo", "${x:-a)b}"}, "", 15},
		{"redirection inside a parameter expansion", "echo ${x:->(a} ", -1, []string{"echo", "${x:->(a}"}, "", 15},
		{"current word in a parameter expansion", "echo ${x:-a b", -1, []string{"echo"}, "${x:-a b", 5},
		{"substitution inside a parameter expansion", "echo ${x:-$(git ch", -1, []string{"git"}, "ch", 16},
		{"command after a heredoc body", "cat <<EOF\nfoo\nEOF\ngit checkout ma", -1, []string{"git", "checkout"}, "ma", 31},
		{"command after a tab-stripped heredoc body", "cat <<-EOF\n\tfoo\n\tEOF\ngit checkout ma", -1, []string{"git", "checkout"}, "ma", 34},
		{"word in a heredoc body", "cat <<EOF\nfoo ba", -1, nil, "ba", 14},
		{"quote in a heredoc body is text", "cat <<EOF\ndon't ba", -1, nil, "ba", 16},
		{"heredoc body word is raw", "cat <<EOF\nit's", -1, nil, "it's", 10},
		{"apostrophe in a comment opens no quote", "echo hi # don't x", -1, nil, "x", 16},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pos := tt.pos
			if pos < 0 {
				pos = len(tt.line)
			}
			got := parseCommandWords(tt.line, pos)
			if strings.Join(got.words, "\x00") != strings.Join(tt.words, "\x00") || len(got.words) != len(tt.words) {
				t.Errorf("words = %q, want %q", got.words, tt.words)
			}
			if got.current != tt.current {
				t.Errorf("current = %q, want %q", got.current, tt.current)
			}
			if got.currentStart != tt.start {
				t.Errorf("currentStart = %d, want %d", got.currentStart, tt.start)
			}
		})
	}
}

func TestParseCommandWords_Position(t *testing.T) {
	tests := []struct {
		line string
		want wordPosition
	}{
		{"gi", positionCommand},
		{"git ", positionArgument},
		{"git checkout ma", positionArgument},
		{"git checkout > ma", positionRedirectTarget},
		{"git checkout >ma", positionRedirectTarget},
		{"git checkout 2>", positionRedirectTarget},
		{"git checkout > ", positionRedirectTarget},
		{"git checkout > out ma", positionArgument},
		{"> ou", positionRedirectTarget},
		{"> out gi", positionCommand},
		{"diff <(ls", positionCommand},
		{"diff <(ls -", positionArgument},
		{"diff <(", positionCommand},
		{"diff <(ls) ", positionArgument},
		{"echo '>' x", positionArgument},
		{"echo hi>ou", positionRedirectTarget},
		{"echo hi> ou", positionRedirectTarget},
		{"cat <<< ou", positionRedirectTarget},
		{"cat <<<ou", positionRedirectTarget},
		{"cat <<< out ma", positionArgument},
		{"x+=1 gi", positionCommand},
		{"x[0]=1 gi", positionCommand},
		{"cat <<EOF\nma", positionText},
		{"cat <<EOF\n", positionText},
		{"cat <<EOF\nfoo ma", positionText},
		{"cat <<EOF\nfoo\nEO", positionText},
		{"cat <<EOF\nfoo\nEOF\n", positionCommand},
		{"cat <<EOF\nfoo\nEOF\ncat ", positionArgument},
		{"cat <<EOF; ec", positionCommand},
		{"kubectl get # po", positionText},
		{"# gi", positionText},
		{"echo hi #", positionText},
		{"echo hi # a b", positionText},
		{"echo hi # a\ngi", positionCommand},
		{"echo `ls # a` x", positionArgument},
		{"echo \\>x", positionArgument},
	}
	for _, tt := range tests {
		if got := parseCommandWords(tt.line, len(tt.line)).position; got != tt.want {
			t.Errorf("parseCommandWords(%q).position = %d, want %d", tt.line, got, tt.want)
		}
	}
}

// FuzzParseCommandWords checks that splitting never panics and that the
// current word is the text between currentStart and the cursor.
func FuzzParseCommandWords(f *testing.F) {
	for _, seed := range []string{
		"echo $(date) fo", "ls $(pwd)/", "cat <( (ls) ) fo", "echo `a $(b` c",
		"x=$(( $(echo 1) + 2 )) y", `echo "$(echo ")" fo`, "a >| b 2>&1 c", "(( a < b )) && c",
		"diff <(ls) >(cat", "echo $(case a in a) b;; esac) c", "echo ${x:-a b} c", "x=${y:-$(a b)} c",
	} {
		f.Add(seed, len(seed))
	}
	f.Fuzz(func(t *testing.T, line string, pos int) {
		pos = clampCursor(line, pos)
		cw := parseCommandWords(line, pos)
		if cw.currentStart < 0 || cw.currentStart > pos {
			t.Fatalf("parseCommandWords(%q, %d).currentStart = %d", line, pos, cw.currentStart)
		}
		if raw := line[cw.currentStart:pos]; cw.current != raw && cw.current != shlex.Unquote(raw) {
			t.Fatalf("parseCommandWords(%q, %d).current = %q, raw %q", line, pos, cw.current, raw)
		}
	})
}
