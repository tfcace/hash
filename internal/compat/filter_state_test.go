package compat

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/executor"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func filterBash(t *testing.T, content string) (string, *Report) {
	t.Helper()
	rcFile := filepath.Join(t.TempDir(), ".zshrc")
	if err := os.WriteFile(rcFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	filtered, report, err := FilterWithDialect(rcFile, "zsh", "bash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(filtered), rcFile); err != nil {
		t.Fatalf("filtered file should parse as bash, got %v\nfiltered:\n%s", err, filtered)
	}
	return filtered, report
}

func filteredLine(t *testing.T, filtered string, n int) string {
	t.Helper()
	lines := strings.Split(filtered, "\n")
	if n >= len(lines) {
		t.Fatalf("filtered has %d lines, want line %d:\n%s", len(lines), n, filtered)
	}
	return strings.TrimSpace(lines[n])
}

func isNeutral(line string) bool {
	return strings.HasPrefix(line, "#") || strings.HasPrefix(line, ": #")
}

// A skipped line ending in a backslash continues onto the next line; that
// line is part of the skipped command and must not run on its own.
func TestFilter_SkippedLineContinuationStaysSkipped(t *testing.T) {
	filtered, _ := filterBash(t, `zstyle ':completion:*' matcher-list \
  'm:{a-z}={A-Z}' \
  'r:|=*'
eval "$(zoxide init zsh)" \
  && echo after
export HASH_KEPT=1
`)
	for n := 0; n <= 4; n++ {
		if line := filteredLine(t, filtered, n); !isNeutral(line) {
			t.Errorf("line %d = %q, want it neutralized with its skipped command", n+1, line)
		}
	}
	if line := filteredLine(t, filtered, 5); line != "export HASH_KEPT=1" {
		t.Errorf("line 6 = %q, want it kept", line)
	}
}

// A skipped command that opens a quote spans lines until the quote closes.
func TestFilter_SkippedLineOpenQuoteStaysSkipped(t *testing.T) {
	filtered, _ := filterBash(t, `zstyle ':x' format "first
second line"
export HASH_KEPT=1
`)
	if line := filteredLine(t, filtered, 1); !isNeutral(line) {
		t.Errorf("line 2 = %q, want it neutralized", line)
	}
	if line := filteredLine(t, filtered, 2); line != "export HASH_KEPT=1" {
		t.Errorf("line 3 = %q, want it kept", line)
	}
}

// A one-line if that is skipped is complete: rewriting it to
// `if false; then # ...` lost its fi and broke the rest of the file.
func TestFilter_SkippedOneLineIfStaysBalanced(t *testing.T) {
	filtered, _ := filterBash(t, `if [[ -n "${(%):-%n}" ]]; then echo hi; fi
export HASH_KEPT=1
`)
	if line := filteredLine(t, filtered, 0); !isNeutral(line) {
		t.Errorf("line 1 = %q, want a no-op", line)
	}
}

// A zsh-only elif is dropped with the branches after it, since its test
// picks which of them runs; the branches before it stay.
func TestFilter_SkippedElifDropsTheBranchesAfterIt(t *testing.T) {
	filtered, report := filterBash(t, `if [ -n "$HASH_A" ]; then
  echo a
elif [[ "${(%):-%n}" == root ]]
then
  echo b
fi
if [ -n "$HASH_A" ]; then
  echo a
elif [[ "${(%):-%n}" == root ]]; then
  echo b
fi
`)
	if report.Summary.Skipped != 2 {
		t.Errorf("Skipped = %d, want 2", report.Summary.Skipped)
	}
	for _, n := range []int{2, 3, 4, 8, 9} {
		if line := filteredLine(t, filtered, n); !strings.HasPrefix(line, skipComment) {
			t.Errorf("line %d = %q, want it commented out with the elif", n+1, line)
		}
	}
	if out := runFiltered(t, "HASH_A=1\n"+filtered); out != "a\na\n" {
		t.Errorf("output = %q, want the if branches only\nfiltered:\n%s", out, filtered)
	}
	if out := runFiltered(t, filtered); out != "" {
		t.Errorf("output = %q, want no branch to run\nfiltered:\n%s", out, filtered)
	}
}

// Heredoc bodies and multi-line strings are data, not commands; a $( ) in
// an unquoted body is code, filtered as code.
func TestFilter_HeredocAndMultilineStringAreVerbatim(t *testing.T) {
	content := `cat > /dev/null <<EOF
bindkey -v
eval "$(zoxide init zsh)"
EOF
cat > /dev/null <<-'END'
	setopt AUTO_CD
	END
export HASH_MSG="line one
setopt nothing"
x=$((1<<2))
setopt AUTO_CD
`
	filtered, report := filterBash(t, content)
	want := strings.Split(content, "\n")
	for _, n := range []int{1, 5, 8} {
		if got := strings.Split(filtered, "\n")[n]; got != want[n] {
			t.Errorf("line %d = %q, want verbatim %q", n+1, got, want[n])
		}
	}
	if got := strings.Split(filtered, "\n")[2]; got != `eval "$(:)"` {
		t.Errorf("line 3 = %q, want its $( ) filtered", got)
	}
	if line := filteredLine(t, filtered, 10); !isNeutral(line) {
		t.Errorf("line 11 = %q, want setopt after the heredocs and arithmetic no-oped", line)
	}
	if report.Summary.Skipped != 2 {
		t.Errorf("Skipped = %d, want 2", report.Summary.Skipped)
	}
}

// A comment with an apostrophe does not open a quote.
func TestFilter_ApostropheInCommentIsNotAQuote(t *testing.T) {
	filtered, _ := filterBash(t, `echo hi # don't
setopt AUTO_CD
`)
	if line := filteredLine(t, filtered, 1); !isNeutral(line) {
		t.Errorf("line 2 = %q, want setopt no-oped", line)
	}
}

// A line the scanner could misread must not stop the filtering of the lines
// after it: the zsh-only lines that follow are still neutralized and the
// whole file still parses.
func TestFilter_MisreadableLinesKeepFilteringTheRest(t *testing.T) {
	tail := "setopt autocd\n[[ -n \"${(%):-%n}\" ]] && echo hi\nexport KEPT=1\n"
	for name, first := range map[string]string{
		"shift in arithmetic":   "x=1; y=2\n(( z = x << y ))\n",
		"comment after ;":       "echo foo;# it's fine\n",
		"quotes inside $( )":    "export X=\"$(echo \"it's\")\"\n",
		"shift in $(( ))":       "z=$(( 1 << y ))\n",
		"backticks with quotes": "x=\"`echo \"it's\"`\"\n",
		"heredoc inside $( )":   "x=$(cat <<EOF\nit's\nEOF\n)\n",
		"comment after (":       "(echo a)# it's\n",
		"nested $( ) in quotes": "x=\"$(echo \"$(echo \"it's\")\")\"\n",
		"case inside $( )":      "x=$(case a in a) echo \"it's\";; esac)\n",
		"base in $(( ))":        "x=$(( 16#ff ))\n",
	} {
		t.Run(name, func(t *testing.T) {
			filtered, _ := filterBash(t, first+tail)
			lines := strings.Split(filtered, "\n")
			n := strings.Count(first, "\n")
			for i := n; i < n+2; i++ {
				if !isNeutral(strings.TrimSpace(lines[i])) {
					t.Errorf("line %d = %q, want it neutralized\nfiltered:\n%s", i+1, lines[i], filtered)
				}
			}
			if got := lines[n+2]; got != "export KEPT=1" {
				t.Errorf("line %d = %q, want it kept", n+3, got)
			}
		})
	}
}

// A skipped one-line if is complete even when more commands follow its fi:
// a command after ; still runs, one after && is skipped with the if, whose
// status it depends on.
func TestFilter_SkippedOneLineIfFollowedByCommandsStaysBalanced(t *testing.T) {
	for line, want := range map[string]string{
		`if [[ -n "${(%):-%n}" ]]; then echo hi; fi; echo after`:   "after\n",
		`if [[ -n "${(%):-%n}" ]]; then echo hi; fi && echo after`: "",
		`while [[ -n "${(%):-%n}" ]]; do break; done; echo after`:  "after\n",
	} {
		filtered, _ := filterBash(t, line+"\nexport HASH_KEPT=1\n")
		if got := runFiltered(t, filtered); got != want {
			t.Errorf("%s: output %q, want %q\nfiltered:\n%s", line, got, want, filtered)
		}
	}
}

// A # glued to the word before it is not a comment, even when that word ends
// in an escaped blank or a $( ): there the quote after the # opens a string,
// and the lines inside it are data.
func TestFilter_HashInsideAWordIsNotAComment(t *testing.T) {
	for name, first := range map[string]string{
		"after an escaped blank": `echo a\ #"`,
		"after $( )":             `x=$(echo a)#"`,
	} {
		t.Run(name, func(t *testing.T) {
			filtered, _ := filterBash(t, first+"\nsetopt autocd\n\"\nexport HASH_KEPT=1\n")
			if line := strings.Split(filtered, "\n")[1]; line != "setopt autocd" {
				t.Errorf("line 2 = %q, want it verbatim inside the string\nfiltered:\n%s", line, filtered)
			}
		})
	}
}

// Keywords, separators and comments inside quotes are text: a skipped
// compound whose quotes hold fi, ; or # keeps its structure.
func TestFilter_SkippedCompoundIgnoresQuotedKeywords(t *testing.T) {
	for name, content := range map[string]string{
		"quoted fi":        "if [[ -n \"${(%):-%n}\" ]]; then echo 'not fi yet'\n  echo body\nfi\n",
		"quoted ;":         "if [[ \"${(%):-%n}\" == \"a;b\" ]]; then\n  echo body\nfi\n",
		"quoted #":         "if [[ \"${(%):-%n}\" == \"a #b\" ]]; then echo hi; fi\n",
		"quoted done":      "while [[ -n \"${(%):-%n}\" ]]; do echo \"x done y\"\n  break\ndone\n",
		"comment after if": "if [[ -n \"${(%):-%n}\" ]]; then # fi\n  echo body\nfi\n",
	} {
		t.Run(name, func(t *testing.T) {
			filtered, _ := filterBash(t, content+"export HASH_KEPT=1\n")
			if line := filteredLine(t, filtered, 0); !strings.Contains(line, "# [hash-compat]") {
				t.Errorf("line 1 = %q, want it skipped", line)
			}
		})
	}
}

// A quoted heredoc delimiter may hold blanks: the body runs to the line
// that is the whole delimiter.
func TestFilter_QuotedHeredocDelimiterWithBlank(t *testing.T) {
	filtered, _ := filterBash(t, "cat > /dev/null <<\"END OF\"\nsetopt autocd\nEND OF\nsetopt autocd\nexport HASH_KEPT=1\n")
	lines := strings.Split(filtered, "\n")
	if lines[1] != "setopt autocd" {
		t.Errorf("line 2 = %q, want the heredoc body verbatim\nfiltered:\n%s", lines[1], filtered)
	}
	if !isNeutral(lines[3]) {
		t.Errorf("line 4 = %q, want setopt after the heredoc no-oped\nfiltered:\n%s", lines[3], filtered)
	}
}

// A command starts right after an opening backtick, so a # there is a
// comment and the quote in it opens nothing.
func TestFilter_CommentAtStartOfBackticks(t *testing.T) {
	filtered, _ := filterBash(t, "x=`#'`\nsetopt autocd\n# ' `\nexport HASH_KEPT=1\n")
	if line := filteredLine(t, filtered, 1); !isNeutral(line) {
		t.Errorf("line 2 = %q, want setopt no-oped\nfiltered:\n%s", line, filtered)
	}
}

// An arithmetic command ends at its )), so a # right after it starts a
// comment (unlike after $(( )), which is part of a word).
func TestFilter_CommentAfterArithmeticCommand(t *testing.T) {
	filtered, _ := filterBash(t, "(( x = 1 ))# it's\nsetopt autocd\n# '\nexport HASH_KEPT=1\n")
	if line := filteredLine(t, filtered, 1); !isNeutral(line) {
		t.Errorf("line 2 = %q, want setopt no-oped\nfiltered:\n%s", line, filtered)
	}
}

// A line continuation joins the lines before the shell reads words, so a #
// that starts the continued line still starts a comment.
func TestFilter_CommentAfterLineContinuation(t *testing.T) {
	filtered, _ := filterBash(t, "echo a \\\n#'b\nsetopt autocd\n# '\nexport HASH_KEPT=1\n")
	if line := filteredLine(t, filtered, 2); !isNeutral(line) {
		t.Errorf("line 3 = %q, want setopt no-oped\nfiltered:\n%s", line, filtered)
	}
}

// A skipped line that opens a multi-line array (typeset is a no-op) takes
// the array's lines with it; otherwise the lone ) is a syntax error and the
// whole file runs nothing.
func TestFilter_SkippedLineOpeningMultilineArray(t *testing.T) {
	filtered, _ := filterBash(t, "typeset -aU path=(\n  ~/bin\n)\nexport KEEP=1\n")
	for n := 0; n < 3; n++ {
		if line := filteredLine(t, filtered, n); !isNeutral(line) {
			t.Errorf("line %d = %q, want it neutralized", n+1, line)
		}
	}
	if line := filteredLine(t, filtered, 3); line != "export KEEP=1" {
		t.Errorf("line 4 = %q, want it kept", line)
	}
}

// A subshell spanning lines is one construct: a skipped line inside it is
// still checked as a command of its own only when the subshell's first line
// is, so the lines pass through with it.
func TestFilter_MultilineSubshellIsOneConstruct(t *testing.T) {
	filtered, _ := filterBash(t, "(\n  cd /tmp\n  echo 'it''s'\n)\nexport KEEP=1\n")
	if !strings.Contains(filtered, "(\n  cd /tmp\n  echo 'it''s'\n)\nexport KEEP=1") {
		t.Errorf("subshell should pass through verbatim:\n%s", filtered)
	}
}

// runFiltered runs filtered bash with a timeout and returns its output.
func runFiltered(t *testing.T, filtered string) string {
	t.Helper()
	out, _ := runStatus(t, filtered)
	return out
}

// runStatus runs bash source with a timeout, as hash runs a startup file
// (sourced: a return at its top level ends it), and returns its output and
// its exit status.
func runStatus(t *testing.T, src string) (string, int) {
	t.Helper()
	if _, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(src), ""); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	runner, err := interp.New(interp.StdIO(nil, &out, &out), sourcedText(src))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = runner.Run(ctx, sourceCall())
	if err != nil && ctx.Err() != nil {
		t.Fatalf("did not finish: %v\n%s", err, src)
	}
	var status interp.ExitStatus
	if errors.As(err, &status) {
		return out.String(), int(status)
	}
	return out.String(), 0
}

// sourcedPath is the file sourceCall sources, whose text sourcedText serves.
const sourcedPath = "/hash-compat-test/rc"

// sourceCall is the program `source sourcedPath`.
func sourceCall() *syntax.File {
	prog, err := syntax.NewParser().Parse(strings.NewReader("source "+sourcedPath+"\n"), "")
	if err != nil {
		panic(err)
	}
	return prog
}

// sourcedText is the open handler that serves src for sourcedPath.
func sourcedText(src string) interp.RunnerOption {
	return interp.OpenHandler(func(ctx context.Context, path string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
		if path == sourcedPath {
			return readOnly{strings.NewReader(src)}, nil
		}
		return interp.DefaultOpenHandler()(ctx, path, flag, perm)
	})
}

type readOnly struct{ *strings.Reader }

func (readOnly) Write([]byte) (int, error) { return 0, errors.New("read-only") }
func (readOnly) Close() error              { return nil }

// A skipped loop condition must keep the body from running: while gets
// false, and until gets true (until false would loop forever).
func TestFilter_SkippedLoopConditionsNeverRunTheBody(t *testing.T) {
	filtered, _ := filterBash(t, `n=0
until [[ -n "${(%):-%n}" ]]; do
  echo until-ran; n=$((n+1)); [ $n -gt 3 ] && break
done
until [[ -n "${(%):-%n}" ]]
do
  echo until-ran; n=$((n+1)); [ $n -gt 6 ] && break
done
while [[ -n "${(%):-%n}" ]]; do
  echo while-ran; break
done
echo end
`)
	if out := runFiltered(t, filtered); out != "end\n" {
		t.Errorf("output = %q, want only end\nfiltered:\n%s", out, filtered)
	}
}

// zsh-only syntax must not make the whole file fail to parse: the statement
// that holds it is commented out, recorded as skipped, and the rest of the
// file still runs. A function whose } ends its last command, as zsh allows,
// gets the ; bash needs.
func TestFilter_UnparsableStatementsAreCommentedOut(t *testing.T) {
	filtered, report := filterBash(t, `echo first
for f in ~/.zsh/*.zsh(N); do
  echo "loop $f"
done
[[ $TERM == (#i)xterm* ]] && echo term
export FPATH="${(j.:.)fpath}"
precmd() { echo -n ".." }
echo last
`)
	if out := runFiltered(t, filtered); out != "first\nlast\n" {
		t.Errorf("output = %q, want first and last only\nfiltered:\n%s", out, filtered)
	}
	var lines []int
	for _, item := range report.SkippedItems {
		lines = append(lines, item.Line)
	}
	if want := []int{2, 5, 6}; !slices.Equal(lines, want) {
		t.Errorf("skipped lines = %v, want %v (%+v)", lines, want, report.SkippedItems)
	}
	if line := filteredLine(t, filtered, 6); line != `precmd() { echo -n ".."; }` {
		t.Errorf("line 7 = %q, want the ; before }", line)
	}
}

// The report counts the functions the filtered file defines; ones on skipped
// lines do not count.
func TestFilter_CountsFunctions(t *testing.T) {
	_, report := filterBash(t, `f() { echo f; }
function g {
  echo g
}
precmd() { print -Pn ".." }
if [[ -n ${(%):-x} ]]; then h() { :; }; fi
alias a=b
`)
	var names []string
	for _, item := range report.ImportedItems {
		if item.Type == ItemFunction {
			names = append(names, item.Name)
		}
	}
	if report.Summary.Functions != 3 || !slices.Equal(names, []string{"f", "g", "precmd"}) {
		t.Errorf("Functions = %d %v, want 3 [f g precmd]", report.Summary.Functions, names)
	}
}

// A skipped builtin is recorded with its line as written, command included.
func TestFilter_SkippedBuiltinKeepsItsCommand(t *testing.T) {
	_, report := filterBash(t, "zmodload zsh/complist\n  setopt   autocd\n")
	want := []SkippedItem{
		{Line: 1, Content: "zmodload zsh/complist", Reason: "zsh module loading"},
		{Line: 2, Content: "setopt   autocd", Reason: "zsh shell option (use Hash config)"},
	}
	for i := range report.SkippedItems {
		report.SkippedItems[i].File = ""
	}
	if !slices.Equal(report.SkippedItems, want) {
		t.Errorf("SkippedItems = %+v, want %+v", report.SkippedItems, want)
	}
}

// typeset is declare in bash: a declaration with flags the interpreter
// takes runs (and may span lines), one with a zsh-only flag is skipped.
func TestFilter_TypesetKeptUnlessZshOnly(t *testing.T) {
	filtered, report := filterBash(t, `typeset -gx HASH_T_EDITOR=vim
typeset -U path PATH
typeset -a dirs=(
  a b
)
typeset -A m
echo "$HASH_T_EDITOR ${dirs[@]}"
`)
	if len(report.SkippedItems) != 1 || report.SkippedItems[0].Line != 2 {
		t.Errorf("SkippedItems = %+v, want only line 2", report.SkippedItems)
	}
	if out := runFiltered(t, filtered); out != "vim a b\n" {
		t.Errorf("output = %q, want %q\nfiltered:\n%s", out, "vim a b\n", filtered)
	}
}

// zsh's anonymous functions () { ... } have no name; counting functions
// must not trip over them (it dereferenced the missing name).
func TestFilter_AnonymousZshFunction(t *testing.T) {
	_, report := filterRC("() { echo hi; }\nf() { :; }\n", "zsh", "zsh", func() *Report { return NewReport("", "") })
	if report.Summary.Functions != 1 {
		t.Errorf("Functions = %d, want 1", report.Summary.Functions)
	}
}

// mvdan's zsh parser panics on some heredoc words (cat <<$a[); an rc file
// with one is not loaded, with a reason, and startup goes on.
func TestFilter_ParserPanicSkipsTheFile(t *testing.T) {
	out, report := filterRC("cat <<$a[\nx\n", "zsh", "zsh", func() *Report { return NewReport("", "") })
	for line := range strings.SplitSeq(out, "\n") {
		if line != "" && !strings.HasPrefix(line, skipComment) {
			t.Errorf("out = %q, want nothing of the file but comments", out)
		}
	}
	if len(report.NotLoaded) != 1 || report.NotLoaded[0].Load != executor.LoadedNone || report.NotLoaded[0].Reason == "" {
		t.Errorf("NotLoaded = %+v, want the file not loaded, with why", report.NotLoaded)
	}
}

// Quotes nest inside ${ } within double quotes: the apostrophe in
// "${x:-"it's"}" is quoted text, so the line ends there, and the string
// that spans the next lines is still read whole (setopt in it is text).
func TestFilter_QuotesInsideParamInDoubleQuotes(t *testing.T) {
	filtered, report := filterBash(t, "echo \"${x:-\"it's\"}\"\nX=\"a\nsetopt b\"\necho \"${x#\"(\"}$X\"\n")
	if out := runFiltered(t, filtered); out != "it's\na\nsetopt b\n" {
		t.Errorf("output = %q\nfiltered:\n%s", out, filtered)
	}
	if len(report.SkippedItems) != 0 {
		t.Errorf("SkippedItems = %+v, want none", report.SkippedItems)
	}
}

// The code a skipped statement guards never runs: `cond && { }` is skipped
// with its group, a dangling && or || with its right side, a case with its
// arms. A compound the zsh parser rejects, or reads as a command and then
// meets its closer, stops the file before it runs any of the body.
func TestFilter_GuardedCodeOfSkippedLinesNeverRuns(t *testing.T) {
	for _, src := range []string{
		"[[ -n \"${(%):-%n}\" ]] && {\n  echo GUARDED\n}\n",
		"[[ -n \"${(%):-%n}\" ]] &&\n  echo GUARDED\n",
		"[[ -n \"${(%):-%n}\" ]] ||\n  echo GUARDED\n",
		"case ${(%):-%n} in\n  root)\n    echo GUARDED\n    ;;\nesac\n",
		"[[ $TERM == (#i)dumb ]] && { echo GUARDED\n  echo GUARDED; }\n",
		"(( ${+_comps[foo]} )) ||\n  echo GUARDED\n",
		"function zle-line-init zle-keymap-select {\n  echo GUARDED\n}\n",
		"function zle-line-init zle-keymap-select\n{\n  echo GUARDED\n}\n",
		"setopt extendedglob && {\n  echo GUARDED\n}\n",
		"zstyle ':x' format 'a' |\n  echo GUARDED\n",
		"for f (a b) {\n  echo GUARDED\n}\n",
		"[[ -n \"${(%):-%n}\" ]] && \\\n  {\n    echo GUARDED\n  }\n",
		"zstyle ':x' format \"a\nb\" && {\n  echo GUARDED\n}\n",
		"select x in ${(%):-a}; do\n  echo GUARDED\ndone\n",
		"repeat 3 do\n  echo GUARDED\ndone\n",
		"repeat 3; do echo GUARDED; done\n",
		"repeat 3 {\n  echo GUARDED\n}\n",
		"for f in ~/.zsh/*.zsh(N); do {\n  echo GUARDED\n}; done\n",
		"[[ $TERM == (#i)dumb ]] || {\n  echo GUARDED\n}\n",
		"if [[ $x == (#i)y ]] {\n  echo GUARDED\n}\n",
		"if [[ $x == (#i)y ]]\nthen\n  echo GUARDED\nfi\n",
		"case $x in\n  (#i)root) echo GUARDED;;\n  (#i)admin)\n    echo GUARDED\n    ;;\nesac\n",
	} {
		filtered, report := filterBash(t, "echo before\n"+src+"echo after\n")
		out := runFiltered(t, filtered)
		whole := len(report.NotLoaded) == 0
		if strings.Contains(out, "GUARDED") || strings.Contains(out, "after\n") != whole || (whole && !strings.Contains(out, "before\n")) {
			t.Errorf("%q:\noutput %q\nfiltered:\n%s\nskipped: %+v, not loaded: %+v", src, out, filtered, report.SkippedItems, report.NotLoaded)
		}
	}
}

// A command whose quote never closes takes the rest of the file with it:
// the file loads up to it, and the report says so.
func TestFilter_ConstructOpenAtEOFStopsTheFile(t *testing.T) {
	out, report := filterRC("echo first\nzstyle ':x' \"open\necho never\n", "zsh", "bash", func() *Report { return NewReport("", "") })
	if got := runFiltered(t, out); got != "first\n" {
		t.Errorf("output %q, want first only\n%s", got, out)
	}
	if len(report.NotLoaded) != 1 || report.NotLoaded[0].Load != executor.LoadedBefore || report.NotLoaded[0].Line != 2 {
		t.Errorf("NotLoaded = %+v, want the file not loaded from line 2", report.NotLoaded)
	}
}

// A heredoc opened before a backtick on the same line has its body after
// the line: a setopt in it is text, not a command to skip.
func TestFilter_HeredocBodyAfterBacktickIsVerbatim(t *testing.T) {
	filtered, report := filterBash(t, "cat <<A; echo `cat <<B\nb\nB\n`\nsetopt x\nA\nsetopt y\n")
	if out := runFiltered(t, filtered); out != "setopt x\nb\n" {
		t.Errorf("output %q\nfiltered:\n%s", out, filtered)
	}
	if len(report.SkippedItems) != 1 || report.SkippedItems[0].Line != 7 {
		t.Errorf("SkippedItems = %+v, want only line 7", report.SkippedItems)
	}
}

// A broken statement that starts with a closer keeps the closer, so the
// compound still closes (done < <(zsh-only) used to make nothing of the file
// run); a closer whose opening line was lost still runs nothing.
func TestFilter_BrokenCloserLineKeepsTheCloser(t *testing.T) {
	filtered, report := filterBash(t, "echo first\nfor f in a; do\n  echo \"in $f\"\ndone < <(print -l ${(f)x})\nif true; then\n  echo then\nelse ${(f)x}\nfi\necho last\n")
	if out := runFiltered(t, filtered); out != "first\nin a\nthen\nlast\n" {
		t.Errorf("output %q\nfiltered:\n%s\nskipped: %+v", out, filtered, report.SkippedItems)
	}
	out, report := filterRC("echo a\nrepeat_ 3 do\n echo b\ndone\n", "zsh", "bash", func() *Report { return NewReport("", "") })
	if runFiltered(t, out) != "" || len(report.NotLoaded) != 1 || report.NotLoaded[0].Load != executor.LoadedNone {
		t.Errorf("stray done: out %q, not loaded %+v, want nothing to run", out, report.NotLoaded)
	}
}

// zsh's global (alias -g) and suffix (alias -s) aliases have no bash
// counterpart: the interpreter's alias rejects them with an error at every
// startup, and they were counted as aliases named "-g G".
func TestFilter_ZshAliasKindsAreSkipped(t *testing.T) {
	filtered, report := filterBash(t, "alias -g G='| grep x'\nalias -s txt=vim\nalias ll='ls -la'\n")
	if out := runFiltered(t, filtered); out != "" {
		t.Errorf("output %q, want no errors\nfiltered:\n%s", out, filtered)
	}
	if report.Summary.Aliases != 1 || report.Summary.Skipped != 2 {
		t.Errorf("summary = %+v, want 1 alias and 2 skipped (%+v)", report.Summary, report.SkippedItems)
	}
}

// add-zsh-hook comes from zsh's autoloaded functions: kept, it failed with
// command not found at every startup.
func TestFilter_AddZshHookIsSkipped(t *testing.T) {
	filtered, report := filterBash(t, "autoload -U add-zsh-hook\nadd-zsh-hook precmd my_precmd\n")
	if line := filteredLine(t, filtered, 1); !isNeutral(line) || report.Summary.Skipped != 2 {
		t.Errorf("line 2 = %q, skipped %+v, want it skipped", line, report.SkippedItems)
	}
}
