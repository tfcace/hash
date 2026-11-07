package completion

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tfcace/hash/internal/shparse"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// A spot is a place in a command line where Tab can insert a value: the
// line up to the cursor, the rest of the program after it, and where the
// value lands when the program runs.
type spot struct {
	name, before, after string
	// wordStart: the completer leaves the replacement to the router, which
	// starts it at the word ("e opens a quote); otherwise it starts at the
	// cursor, inside whatever is open there.
	wordStart bool
	lands     landing
	text      bool // a heredoc body, which cannot hold a newline
	// tail is the text after the value on a heredoc body's line, stripTabs
	// a <<- body's: the line must not be the delimiter EOF, and a <<- body
	// strips its leading tabs.
	tail      string
	stripTabs bool
	deep      bool // two backticks deep, where hash's parser reads backslashes its own way
	// escapes is set where the value follows a backslash the user typed
	// after the letter a: the bytes it escapes there ("*" for any), so that
	// the value's first byte is the escaped one. Before any other byte the
	// backslash is text.
	escapes string
	splits  bool     // hash's parser splits and globs the value, quoted or not
	zsh     bool     // zsh syntax: the spot is only tried in hash's zsh dialect
	lead    []string // the arguments probe gets before the value (a glob's matches)
	// nothing: the lexer and hash's parser read the spot differently (a
	// body line mvdan ends the body at), so no value is offered
	nothing bool
	pattern bool // the value is matched as a pattern, which must be UTF-8
	// closes is what the user types after the value to close a construct
	// the line leaves open (a [[ test, a case pattern): it is not on the
	// line Tab completes, only on the line that runs.
	closes string
	reread bool // the shell parses the value again as code (eval): only plain words are offered
	// misread: the lexer reads the place otherwise than hash's parser (a
	// case pattern's ) for the end of a substitution), so any value may be
	// refused; one offered must still land.
	misread bool
	// framed is probe's argument when the value is only part of it, with
	// the value for its %s.
	framed string
}

// landing is where a value inserted at a spot ends up.
type landing int

const (
	asArgument landing = iota // the argument of the first probe command
	asLine                    // the line cat reads from a heredoc
	asPath                    // the file a redirection opens
	nowhere                   // a comment: probe runs with the argument q alone
	// asMatch: the value is held against $v, which holds the value: probe
	// runs with the argument y when they match
	asMatch
)

var spots = []spot{
	{name: "unquoted", before: "probe "},
	{name: "unquoted, a line after", before: "probe ", after: "\n: x"},
	{name: "in backticks, a line after", before: "echo `probe ", after: "\n: x`"},
	{name: "unquoted after a backslash, a line after", before: `probe a\`, after: "\n: x", escapes: "*"},
	{name: "in double quotes", before: `probe "`, after: `"`},
	{name: "in single quotes", before: `probe '`, after: `'`},
	{name: "in ANSI-C quotes", before: `probe $'`, after: `'`},
	{name: "opening double quotes", before: `probe "`, wordStart: true},
	{name: "opening single quotes", before: `probe '`, wordStart: true},
	{name: "opening ANSI-C quotes", before: `probe $'`, wordStart: true},
	{name: "in $( )", before: "echo $(probe ", after: ")"},
	{name: "in $( ) in double quotes", before: `echo "$(probe `, after: `)"`},
	{name: "in backticks", before: "echo `probe ", after: "`"},
	{name: "in backticks twice", before: "echo `echo \\`probe ", after: "\\``", deep: true},
	{name: "in backticks in double quotes", before: "echo \"`probe ", after: "`\""},
	{name: "in double quotes in backticks", before: "echo `probe \"", after: "\"`"},
	{name: "in single quotes in backticks", before: "echo `probe '", after: "'`"},
	{name: "opening double quotes in backticks", before: "echo `probe \"", after: "`", wordStart: true},
	{name: "opening single quotes in $( )", before: "echo $(probe '", after: ")", wordStart: true},
	{name: "in $( ) in backticks", before: "echo `echo $(probe ", after: ")`"},
	{name: "in backticks in $( )", before: "echo $(echo `probe ", after: "`)"},
	{name: "a comment", before: "probe q # ", lands: nowhere},
	{name: "a comment in backticks", before: "echo `probe q # ", after: "`", lands: nowhere},
	{name: "a comment in $( )", before: "echo $(probe q # ", after: "\n)", lands: nowhere},
	{name: "a glued comment", before: "probe #", wordStart: true},
	{name: "a glued comment in backticks", before: "echo `probe #", after: "`", wordStart: true},
	{name: "a glued comment in $( )", before: "echo $(probe #", after: "\n)", wordStart: true},
	{name: "after an extglob holding #", before: "shopt -s extglob; probe !(#*#) ", lead: []string{"a b", "ab", "abc", "b"}},
	{name: "after zsh glob flags", before: "probe (#i)x ", lead: []string{"(#i)x"}, zsh: true},
	{name: "after zsh glob qualifiers", before: "probe *(#qN) ", lead: []string{"*(#qN)"}, zsh: true},
	{name: "a heredoc body", before: "cat <<EOF\n", after: "\nEOF\n", lands: asLine, text: true},
	{name: "a quoted heredoc body", before: "cat <<'EOF'\n", after: "\nEOF\n", lands: asLine, text: true},
	{name: "a heredoc body in backticks", before: "echo `cat <<EOF\n", after: "\nEOF\n`", lands: asLine, text: true},
	{name: "a quoted heredoc body in backticks", before: "echo `cat <<'EOF'\n", after: "\nEOF\n`", lands: asLine, text: true},
	{name: "a heredoc body in backticks twice", before: "echo `echo \\`cat <<EOF\n", after: "\nEOF\n\\``", lands: asLine, text: true, deep: true},
	{name: "$( ) in a heredoc body", before: "cat <<EOF\n$(probe ", after: ")\nEOF\n"},
	{name: "backticks in a heredoc body", before: "cat <<EOF\n`probe ", after: "`\nEOF\n"},
	{name: "${ } in a heredoc body", before: "cat <<EOF\n${x:-", after: "}\nEOF\n", lands: asLine},
	{name: "${ } in double quotes", before: `probe "${x:-`, after: `}"`},
	{name: "${ } unquoted", before: "probe ${x:-", after: "}", splits: true},
	{name: "unquoted after a backslash", before: `probe a\`, escapes: "*"},
	{name: "in double quotes after a backslash", before: `probe "a\`, after: `"`, escapes: "$`\"\\"},
	{name: "a heredoc body after a backslash", before: "cat <<EOF\na\\", after: "\nEOF\n", lands: asLine, text: true, escapes: "$`\\"},
	{name: "after a body line hash's parser takes for the delimiter", before: "cat <<EOF\n$()EOF\n", after: "\nEOF\n", nothing: true},
	{name: "a heredoc body with text after", before: "cat <<EOF\n", after: "OF\nEOF\n", lands: asLine, text: true, tail: "OF"},
	{name: "a <<- heredoc body", before: "cat <<-EOF\n", after: "\nEOF\n", lands: asLine, text: true, stripTabs: true},
	{name: "a <<- heredoc body after a tab", before: "cat <<-EOF\n\t", after: "\nEOF\n", lands: asLine, text: true, stripTabs: true},
	{name: "a <<- quoted heredoc body", before: "cat <<-'EOF'\n", after: "\nEOF\n", lands: asLine, text: true, stripTabs: true},
	{name: "a redirection target", before: "probe q > ", lands: asPath},
	{name: "a glued redirection target", before: "probe q 2>", lands: asPath},
	{name: "a redirection target in double quotes", before: `probe q > "`, after: `"`, lands: asPath},
	{name: "a redirection target opening double quotes", before: `probe q > "`, wordStart: true, lands: asPath},
	{name: "an assignment", before: "x=", after: `; probe "$x"`},
	{name: "an assignment in double quotes", before: `x="`, after: `"; probe "$x"`},
	{name: "an exported assignment", before: "export x=", after: `; probe "$x"`},
	{name: "an assignment in backticks", before: "echo `x=", after: "; probe \"$x\"`"},
	{name: "an appending assignment", before: "x+=", after: `; probe "$x"`},
	{name: "an exported appending assignment", before: "export x+=", after: `; probe "$x"`},
	{name: "an array element assignment", before: "x[0]=", after: `; probe "${x[0]}"`},
	{name: "a here-string", before: "cat <<< ", lands: asLine},
	{name: "a glued here-string", before: "cat <<<", lands: asLine},
	{name: "a [[ test operand", before: "[[ ", after: ` == "$v" ]] && probe y`, lands: asMatch},
	{name: "a [[ test operand after &&", before: "[[ -n x && ", after: ` == "$v" ]] && probe y`, lands: asMatch},
	{name: "a case subject", before: "case ", after: ` in "$v") probe y;; esac`, lands: asMatch},
	{name: "a [[ pattern", before: "[[ $v == ", after: " ]] && probe y", lands: asMatch, pattern: true},
	{name: "a case pattern", before: "case $v in ", after: ") probe y;; esac", lands: asMatch, pattern: true},
	{name: "an unclosed [[ test", before: "[[ ", closes: ` == "$v" ]] && probe y`, lands: asMatch},
	{name: "an unclosed [[ test in double quotes", before: `[[ "`, closes: `" == "$v" ]] && probe y`, lands: asMatch},
	{name: "an unclosed [[ test in an if", before: "if [[ -n x && ", closes: ` == "$v" ]]; then probe y; fi`, lands: asMatch},
	{name: "an unclosed [[ test in a while", before: "while [[ ", closes: ` == "$v" ]]; do probe y; break; done`, lands: asMatch},
	{name: "an unclosed [[ pattern", before: "[[ $v == ", closes: " ]] && probe y", lands: asMatch, pattern: true},
	{name: "an unclosed case pattern", before: "case $v in ", closes: ") probe y;; esac", lands: asMatch, pattern: true},
	{name: "in $( ) in an unclosed [[ test", before: "[[ -z $(probe ", closes: ") ]]"},
	{name: "an argument of eval", before: "eval probe ", reread: true},
	{name: "an argument of eval in double quotes", before: `eval "probe `, after: `"`, reread: true},
	{name: "an argument of eval in a substitution", before: `eval "$(echo probe `, after: `)"`, reread: true},
	{name: "a trap's action", before: `trap "probe `, after: `" EXIT`, reread: true},
	{name: "a command after a comment that ends in a backslash", before: "eval # x \\\n probe "}, // the backslash is the comment's (shparse)
	{name: "an unclosed [[ test after a newline", before: "[[ \n  ", closes: ` == "$v" ]] && probe y`, lands: asMatch},
	{name: "a redirection target in a case arm's quotes", before: `probe "$(case $x in *) printf %s "; cat > `, after: `" ;; esac)"`, framed: "; cat > %s", misread: true},
	{name: "an assignment in a case arm's quotes", before: `probe "$(case $x in *) printf %s "; x=`, after: `" ;; esac)"`, framed: "; x=%s", misread: true},
	{name: "an argument in a case arm's quotes", before: `probe "$(case $x in *) printf %s "; ls `, after: `" ;; esac)"`, framed: "; ls %s", misread: true},
	{name: "a redirection target in a case arm's backticks", before: "probe \"$(case $x in *) printf %s `; cat > ", after: "` ;; esac)\"", framed: "%s", misread: true},
}

// readBackValues are values a completer may offer: file and branch names
// hold anything but NUL (and, for branches, a few more).
var readBackValues = []string{
	"a b", "a;id", "$(id)", "`id`", "a'b", `a"b`, `a\b`, `\`, "x\\", `\$x`, "*", "#x", "!x", "a|b&c",
	"x>y", "{a,b}", "a}b", "a)b", "a\tb", "ünï cödé", "$HOME", "$x", "${x}", "$'x'", "a\nb", "a\r\nb",
	"a\rb", "a\r", "\x1b[0m", "~", "~root", "a:~/x", "=ls", "\xff\xfe", "a\xffb", "a\x00b", "\xe2\x82", "a ?? b", "-n", "[ab]", "a*", "a?", "a[b]c",
	"a\\\nb", `"$(id)"`, "'", "dir/", "a b/", "a$", "$", "plain.txt", "EOF", "E", "\tx", "\tEOF",
}

// TestRouter_InsertedValueReadsBack inserts every value at every spot and
// runs the line, parsed and expanded as hash runs it (mvdan's bash and zsh
// dialects): the value must land as itself, and nothing else may run. A
// value the spot cannot hold (a newline in a comment) is not offered.
func TestRouter_InsertedValueReadsBack(t *testing.T) {
	for _, lang := range []syntax.LangVariant{syntax.LangBash, syntax.LangZsh} {
		for _, s := range spots {
			if s.zsh && lang != syntax.LangZsh {
				continue
			}
			for _, value := range readBackValues {
				if msg := s.readBack(t, lang, value); msg != "" {
					t.Errorf("[%s] %s, value %q: %s", lang, s.name, value, msg)
				}
			}
		}
	}
}

// FuzzInsertedValueReadsBack is TestRouter_InsertedValueReadsBack for any
// value at any spot.
func FuzzInsertedValueReadsBack(f *testing.F) {
	for i, value := range readBackValues {
		f.Add(value, uint8(i), i%2 == 0)
	}
	f.Fuzz(func(t *testing.T, value string, which uint8, zsh bool) {
		if value == "" {
			return
		}
		s := spots[int(which)%len(spots)]
		lang := syntax.LangBash
		if zsh || s.zsh {
			lang = syntax.LangZsh
		}
		if msg := s.readBack(t, lang, value); msg != "" {
			t.Fatalf("[%s] %s, value %q: %s", lang, s.name, value, msg)
		}
	})
}

// oneValue completes any line with one value, replacing from the cursor or,
// with wordStart, leaving the start to the router.
type oneValue struct {
	value     string
	wordStart bool
}

func (c oneValue) Name() string { return "one" }

func (c oneValue) Complete(_ context.Context, _ string, pos int) (Result, error) {
	from := pos
	if c.wordStart {
		from = 0
	}
	return Result{Items: []Item{{Value: c.value}}, ReplaceFrom: from}, nil
}

// mayRefuse reports whether the spot may leave value out: a NUL no shell
// word holds, a newline, a carriage return that may end a line or bytes
// that are not UTF-8 in text, where no quoting applies, anything two
// backticks deep, and blanks and glob characters where they split. In a
// heredoc body a line that is the delimiter, or a tab <<- strips.
func (s spot) mayRefuse(value string) bool {
	switch {
	case strings.ContainsRune(value, 0):
		return true
	case s.lands == asLine && s.text && strings.TrimLeft(value+s.tail, "\t") == "EOF",
		s.stripTabs && value[0] == '\t':
		return true
	case s.deep:
		// hash's parser reads backslashes its own way two backticks deep,
		// and places the nodes there off their bytes, so the read-back check
		// cannot hold a value there to the line it was meant for
		return true
	case s.text && strings.Contains(s.before, "`") && value[0] == '\\':
		// a heredoc body in backticks that starts with an escape: hash's
		// parser places it off its bytes
		return true
	case s.splits && strings.ContainsAny(value, " \t\n*?["):
		return true
	case s.escapes != "" && (value[0] == '\n' || value[0] == '\r' || value[0] >= utf8.RuneSelf):
		return true // a backslash would join the next line, or take one byte of a rune
	case strings.Contains(s.before, "[[") && !s.pattern && len(value) == 2 && value[0] == '-':
		return true // [[ reads -n as a test's operator
	case s.pattern && !utf8.ValidString(value):
		return true // hash's interpreter fails on a pattern that is not UTF-8
	case s.reread && strings.TrimFunc(value, plainWordRune) != "":
		return true
	}
	return s.text && (strings.Contains(value, "\n") || strings.HasSuffix(value, "\r") || !utf8.ValidString(value))
}

// readBack inserts value at s and reports what goes wrong when the line
// runs, or "".
func (s spot) readBack(t *testing.T, lang syntax.LangVariant, value string) string {
	router := NewRouter()
	if err := router.SetDialect(lang.String()); err != nil {
		t.Fatal(err)
	}
	router.Register(oneValue{value, s.wordStart}, PriorityPlugin)
	line := s.before + s.after
	result, _ := router.Complete(context.Background(), line, len(s.before))
	if len(result.Items) != 1 {
		if s.nothing || s.misread || s.mayRefuse(value) {
			return ""
		}
		return fmt.Sprintf("not offered (%d items)", len(result.Items))
	}
	if s.nothing {
		return fmt.Sprintf("offered %q", result.Items[0].Value)
	}
	inserted := result.Items[0].Value
	after := s.after
	if s.wordStart && s.after == "" && strings.HasSuffix(value, "/") && strings.ContainsAny(s.before[len(s.before)-1:], `"'`) &&
		inserted != EscapeShellWord(value) {
		after = s.before[len(s.before)-1:] + after // a directory's quote stays open to go on into
	}
	src := line[:result.ReplaceFrom] + inserted + after + s.closes
	got, err := runProbe(t, lang, src, "v="+value)
	if err != nil {
		return fmt.Sprintf("%q: %v", src, err)
	}
	if s.lands == asArgument {
		got = withoutCat(got) // the cat a heredoc feeds
	}
	if want := s.want(value); !slices.Equal(got, want) {
		return fmt.Sprintf("%q ran %q, want %q", src, got, want)
	}
	return ""
}

// want is what runProbe reports when value lands at s.
func (s spot) want(value string) []string {
	switch {
	case s.escapes == "*" || s.escapes != "" && strings.IndexByte(s.escapes, value[0]) >= 0:
		value = "a" + value
	case s.escapes != "":
		value = `a\` + value
	}
	switch s.lands {
	case asLine:
		return []string{"cat", value + s.tail + "\n"}
	case asPath:
		return []string{"> " + value, probeCall("q")}
	case nowhere:
		return []string{probeCall("q")}
	case asMatch:
		return []string{probeCall("y")}
	}
	if s.framed != "" {
		value = strings.TrimRight(fmt.Sprintf(s.framed, value), "\n") // a substitution's trailing newlines go
	}
	return []string{probeCall(append(slices.Clone(s.lead), value)...)}
}

// runProbe runs src as hash would, except that commands only report what
// they got: each probe call as probeCall reports it, each cat call as "cat" and
// the text it reads, each file opened as "> <path>". Echo is a builtin and
// prints nowhere. Any other command (id, touch) is an error. env adds to
// the environment.
func runProbe(t *testing.T, lang syntax.LangVariant, src string, env ...string) ([]string, error) {
	file, err := shparse.Parse(syntax.NewParser(syntax.Variant(lang)), src, "") // as hash parses a line
	if err != nil {
		return nil, err
	}
	dir := t.TempDir()
	for _, name := range []string{"ab", "a b", "abc", "b"} { // for a glob to match
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			return nil, err
		}
	}
	var got []string
	var ran error
	runner, err := interp.New(
		interp.Dir(dir),
		interp.Env(expand.ListEnviron(append([]string{"HOME=" + dir, "PATH="}, env...)...)),
		interp.StdIO(nil, io.Discard, io.Discard),
		interp.ExecHandlers(func(interp.ExecHandlerFunc) interp.ExecHandlerFunc {
			return func(ctx context.Context, args []string) error {
				switch args[0] {
				case "probe":
					got = append(got, probeCall(args[1:]...))
				case "cat":
					var in bytes.Buffer
					if stdin := interp.HandlerCtx(ctx).Stdin; stdin != nil {
						_, _ = io.Copy(&in, stdin)
					}
					got = append(got, "cat", in.String())
				default:
					ran = fmt.Errorf("ran %q", args)
				}
				return nil
			}
		}),
		interp.OpenHandler(func(_ context.Context, path string, _ int, _ os.FileMode) (io.ReadWriteCloser, error) {
			got = append(got, "> "+path)
			return nopFile{}, nil
		}),
	)
	if err != nil {
		return nil, err
	}
	if err := runner.Run(context.Background(), file); err != nil {
		return got, err
	}
	if ran != nil {
		return got, ran
	}
	return got, nil
}

// probeCall is a probe call with args, as runProbe reports it.
func probeCall(args ...string) string {
	return fmt.Sprintf("probe %q", args)
}

// withoutCat is got without its cat calls and what they read.
func withoutCat(got []string) []string {
	var out []string
	for i := 0; i < len(got); i++ {
		if got[i] == "cat" {
			i++
			continue
		}
		out = append(out, got[i])
	}
	return out
}

type nopFile struct{}

func (nopFile) Read([]byte) (int, error)    { return 0, io.EOF }
func (nopFile) Write(p []byte) (int, error) { return len(p), nil }
func (nopFile) Close() error                { return nil }

// FuzzCompletedLineRunsNothingNew varies the line around the cursor too:
// whatever the line, a value Tab offers there runs the same commands, and
// opens as many files, as a plain one (zq) written for the same place. The
// value names a command only where zq does. Both lines run with commands
// and files only recorded.
func FuzzCompletedLineRunsNothingNew(f *testing.F) {
	for i, s := range spots {
		f.Add(s.before, s.after, readBackValues[i%len(readBackValues)], s.wordStart, s.zsh)
	}
	f.Add("echo $(cat <<EOF\n", "\nEOF\n)", "a;id", false, false)
	f.Add("probe \"${x:-", "}\" #", "a}b;id", false, false)
	f.Add("while :; do probe ", "; done", "a b", false, false) // runs until the deadline, both ways
	f.Add("`\\`\n<", "0\\``", " ", true, false)                // a substitution of only <file, which mvdan runs its own way
	f.Add("`\\`\n<", "\\``", "'", true, false)                 // two backticks deep, the quote's $'\'' ran ' as a command
	f.Fuzz(func(t *testing.T, before, after, value string, wordStart, zsh bool) {
		line := before + after
		if value == "" || len(line) > 200 || strings.Contains(line, value) || strings.Contains(line, "zq") {
			return // a command named like the value would read as it
		}
		if dataFlow.MatchString(after) || evaluates.MatchString(line) || strings.Contains(line, ">&") || strings.Contains(line, "<&") {
			return // the value is used again (a variable, eval, an fd to dup): its content decides what runs
		}
		lang := syntax.LangBash
		if zsh {
			lang = syntax.LangZsh
		}
		router := NewRouter()
		_ = router.SetDialect(lang.String())
		router.Register(oneValue{value, wordStart}, PriorityPlugin)
		result, _ := router.Complete(context.Background(), line, len(before))
		if len(result.Items) != 1 {
			return
		}
		pos := len(before)
		if strings.Trim(value, builtinNameBytes) == "" {
			// a plain name cannot add a command, and may name a builtin (.,
			// [, :) that runs another way than zq (after an empty $1 too)
			return
		}
		in, _ := insertionAt(lang, line, result.ReplaceFrom, pos, "")
		head, tail := "", "" // what writes a value its own way, as writeShellText keeps them
		if in.escaped {
			head = value[:1]
		}
		if strings.HasSuffix(value, "/") && len(value) > len(head) {
			tail = "/"
		}
		plain, ok := in.write(head + "zq" + tail)
		if !ok {
			t.Fatalf("%q: offered %q, but zq cannot be written there", line, result.Items[0].Value)
		}
		src := line[:result.ReplaceFrom] + result.Items[0].Value + after
		ref := line[:result.ReplaceFrom] + plain + after
		for _, l := range []string{src, ref} {
			if background.MatchString(l) || readsFile.MatchString(l) || strings.Contains(l, "(") && strings.ContainsAny(l, "<>") {
				// see background; a process substitution's pipe may wait for
				// a reader for good, and mvdan runs $(<file) its own way
				// (`<0' '` runs "0")
				return
			}
		}
		got, gotOpens, gotDone := runRecorded(t, lang, src)
		want, wantOpens, wantDone := runRecorded(t, lang, ref)
		if slices.Equal(want, []string{"parse error"}) {
			return // the line does not run with zq either
		}
		if !gotDone || !wantDone {
			return // a run cut short at the deadline ran as far as it got: nothing to compare
		}
		for i := range want {
			want[i] = strings.ReplaceAll(want[i], "zq", value[len(head):len(value)-len(tail)]) // where zq names a command, the value does
		}
		slices.Sort(got) // a pipeline's commands run in any order
		slices.Sort(want)
		if !slices.Equal(got, want) || gotOpens != wantOpens {
			t.Fatalf("%q ran %q, opened %d; %q ran %q, opened %d", src, got, gotOpens, ref, want, wantOpens)
		}
	})
}

const builtinNameBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.:[]-"

var (
	// a job in the background, or one side of a pipeline, may not have run
	// when the line is done
	background = regexp.MustCompile(`(^|[^&<>])&($|[^&>])|(^|[^|>])\|($|[^|])`)
	dataFlow   = regexp.MustCompile(`\$[A-Za-z_0-9@*#?!{-]`)
	evaluates  = regexp.MustCompile(`eval|source|alias|\(\)|function|(^|[^A-Za-z0-9_])\.([ \t]|$)|trap|let|declare|typeset|export|read`)
	readsFile  = regexp.MustCompile("(\\$\\(|`)[^A-Za-z_;|&]*<")
)

// runRecorded runs src in a temporary directory, its commands only
// recorded by name, builtins included, and
// files opened only counted. A run that does not finish in a moment stops,
// and done is false; commands it starts in the background are waited for.
func runRecorded(t *testing.T, lang syntax.LangVariant, src string) (got []string, opens int, done bool) {
	file, err := shparse.Parse(syntax.NewParser(syntax.Variant(lang)), src, "") // as hash parses a line
	if err != nil {
		return []string{"parse error"}, 0, true
	}
	runner, err := interp.New(
		interp.Dir(t.TempDir()),
		interp.Env(expand.ListEnviron("HOME=/nonexistent", "PATH=")),
		interp.StdIO(nil, io.Discard, io.Discard),
		interp.CallHandler(func(_ context.Context, args []string) ([]string, error) {
			got = append(got, args[0])
			if slices.Contains([]string{"echo", "printf", "pwd", "type", "command"}, args[0]) {
				return []string{":"}, nil // no output for a substitution to run as a command
			}
			return args, nil
		}),
		interp.ExecHandlers(func(interp.ExecHandlerFunc) interp.ExecHandlerFunc {
			return func(context.Context, []string) error { return nil }
		}),
		interp.OpenHandler(func(context.Context, string, int, os.FileMode) (io.ReadWriteCloser, error) {
			opens++
			return nopFile{}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	defer func() {
		if recover() != nil { // mvdan's expand panics on ${0#\x80}, both ways
			got, opens, done = []string{"panic"}, 0, true
		}
	}()
	_ = runner.Run(ctx, file)
	wait, _ := syntax.NewParser().Parse(strings.NewReader("wait"), "")
	_ = runner.Run(ctx, wait) // the commands it started with &
	return got, opens, ctx.Err() == nil
}
