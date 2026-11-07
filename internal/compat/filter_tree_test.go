package compat

import (
	"context"
	"fmt"
	"github.com/tfcace/hash/internal/executor"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
)

// A skipped statement takes all of itself with it, and nothing else: not
// the else, elif or later case arms of a skipped test, and not the whole
// file over a skipped line that ends a case arm, a typeset with ( ) or a
// (pat) arm.
func TestFilterRC_SkippedStatementsRunNothingElse(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"skipped if, else", "if [[ -n \"${(%):-%n}\" ]]; then\n  echo A\nelse\n  echo B\nfi\necho end\n", "end\n"},
		{"skipped if, elif", "if [[ -n \"${(%):-%n}\" ]]; then\n  echo A\nelif true; then\n  echo C\nfi\necho end\n", "end\n"},
		{"skipped case word, * arm", "case ${(%):-x} in\n  a) echo A ;;\n  *) echo STAR ;;\nesac\necho end\n", "end\n"},
		{"skipped line ending an arm", "export A=1\necho first\ncase \"$OSTYPE\" in\n  darwin*)\n    zstyle ':completion:*' menu select ;;\n  linux*)\n    echo linux ;;\nesac\necho end\n", "first\nend\n"},
		{"if bash cannot parse", "echo first\nif true; then\nfi\necho last\n", "first\nlast\n"},
		{"typeset with ( )", "typeset -U arr=()\necho after\n", "after\n"},
		{"(pat) arm", "echo first\ncase a in\n  (a) print -l ${(f)x} ;;\nesac\necho after\n", "first\nafter\n"},
	} {
		out, rep := filterBash(t, tc.src)
		if got := runFiltered(t, out); got != tc.want {
			t.Errorf("%s: output %q, want %q\nfiltered:\n%s\nskipped: %+v", tc.name, got, tc.want, out, rep.SkippedItems)
		}
	}
}

// The tree filter keeps every line where it was: a skipped statement that
// spans lines leaves them as comments, so later lines keep their numbers
// (errors at startup name the line as written).
func TestFilterRC_KeepsLineNumbers(t *testing.T) {
	src := "if [[ -n ${(%):-x} ]]; then\n  echo a\n\n  echo b\nfi; echo after\nsetopt x; echo c\nbindkey -s '^x' <<EOF && echo d\nsetopt y\nEOF\necho e\n"
	out, _ := filterBash(t, src)
	if got, want := strings.Count(out, "\n"), strings.Count(src, "\n"); got != want {
		t.Errorf("%d lines, want %d\n%s", got, want, out)
	}
	if got := runFiltered(t, out); got != "after\nc\ne\n" {
		t.Errorf("output %q\n%s", got, out)
	}
	if line := filteredLine(t, out, 9); line != "echo e" {
		t.Errorf("line 10 = %q\n%s", line, out)
	}
}

// realisticZshrc returns an rc file of about n lines in the shape of a
// plugin-manager zshrc: exports, aliases, functions, options, zstyles,
// guarded blocks and zsh-only expansions.
func realisticZshrc(n int) string {
	const block = `# --- section %d ---
export PATH="$HOME/bin%d:$PATH"
export EDITOR=nvim
alias g%d='git status'
alias ll%d='ls -la'
setopt autocd extendedglob
zstyle ':completion:*' menu select
bindkey '^R' history-incremental-search-backward
if [[ -r "${XDG_CACHE_HOME:-$HOME/.cache}/p10k-instant-prompt-${(%%):-%%n}.zsh" ]]; then
  source "${XDG_CACHE_HOME:-$HOME/.cache}/p10k-instant-prompt-${(%%):-%%n}.zsh"
fi
f%d() {
  local dir=${1:-.}
  if [ -d "$dir" ]; then
    cd "$dir" && ls
  else
    echo "no such dir: $dir" >&2
  fi
}
case "$OSTYPE" in
  darwin*) export BROWSER=open ;;
  linux*) export BROWSER=xdg-open ;;
esac
[[ -f ~/.fzf.zsh ]] && source ~/.fzf.zsh
for p in ~/.zsh/plugins/*.zsh(N); do
  source "$p"
done
typeset -U path
command -v zoxide >/dev/null && eval "$(zoxide init zsh)"
`
	var b strings.Builder
	for i := 0; strings.Count(b.String(), "\n") < n; i++ {
		fmt.Fprintf(&b, block, i, i, i, i, i)
	}
	return b.String()
}

// What the filter costs on a large rc file.
func BenchmarkFilterRC_Zshrc2000(b *testing.B) {
	src := realisticZshrc(2000)
	newReport := func() *Report { return NewReport("", "") }
	for b.Loop() {
		filterRC(src, "zsh", "bash", newReport)
	}
}

// The realistic rc file of the benchmark takes the tree filter.
func TestFilterRC_RealisticZshrcTakesTheTree(t *testing.T) {
	out, report := filterRC(realisticZshrc(200), "zsh", "bash", func() *Report { return NewReport("", "") })
	if len(report.NotLoaded) > 0 {
		t.Fatalf("not loaded: %+v", report.NotLoaded)
	}
	if _, err := parseRC(out, "bash"); err != nil {
		t.Fatal(err)
	}
}

// The tree filter's rewrites, exactly: skips leave the source as comments,
// heredocs of a skipped command stay open on its ":" with their delimiter
// quoted, zsh's empty bodies and } after a command get what bash needs.
func TestFilterTree_Rewrites(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"skip", "setopt x; echo a\n", ":; echo a\n"},
		{"skip alone", "  setopt x\n", "  : # [hash-compat] setopt x\n"},
		{"skip keeps a comment", "setopt x # mine\n", ": # mine\n"},
		{"multi-line skip", "zstyle ':x' \\\n  y\necho a\n", "# [hash-compat] zstyle ':x' \\\n  : # [hash-compat] y\necho a\n"},
		{"heredoc of a skip", "bindkey -s x <<EOF && echo a\n$(echo b)\nEOF\n", ": <<'EOF' # [hash-compat] bindkey -s x <<EOF && echo a\n$(echo b)\nEOF\n"},
		{"elif drop", "if a; then b; elif [[ ${(%):-x} ]]; then c; else d; fi\n", "if a; then b; fi\n"},
		{"arm drop", "case $x in a) b ;; ${(%):-x}) c ;; *) d ;; esac\n", "case $x in a) b ;; esac\n"},
		{"} after a command", "f() { echo a }\n", "f() { echo a; }\n"},
		{"skipped command before }", "f() { setopt x }\n", "f() { :; }\n"},
		{"empty body", "if true; then\nfi\n", "if true; then :;\nfi\n"},
		{"$x[i]", "echo $x[1]\n", ": # [hash-compat] echo $x[1]\n"},
		{"nested in $( )", "x=$(setopt y; echo a)\n", "x=$(:; echo a)\n"},
		{"bash rejects it", "if true; then\n  echo $((1.5))\nfi\necho c\n", "if true; then\n  : # [hash-compat] echo $((1.5))\nfi\necho c\n"},
	} {
		out, report, err := filterTree(tc.src, "zsh", "bash", func() *Report { return NewReport("", "") })
		if err != nil || out != tc.want {
			t.Errorf("%s: %q, %v, want %q (%+v)", tc.name, out, err, tc.want, report)
		}
	}
}

// A file the zsh parser rejects, or whose skips the tree filter cannot make
// (a skip that holds an earlier heredoc's body), loads up to the statement
// that stops it, as bash and zsh run a file up to its first syntax error:
// the lines before run, the rest are comments on their own lines, and the
// report says where the file stopped loading, and why.
func TestFilterRC_LoadsUpToTheStatementThatStopsIt(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		from      int    // the line it is not loaded from
		ran       string // what the lines before print
	}{
		{"zsh parser rejects it", "echo first\nfor f (a b); do echo $f; done\necho end\n", 2, "first\n"},
		{"it shares a line with the statement before", "echo first\necho same; for f (a b); do echo $f; done\necho end\n", 2, "first\n"},
		{"a statement that never ends", "echo first\necho second\nif true; then\necho end\n", 3, "first\nsecond\n"},
		{"the statement before spans lines", "echo first\nif true; then\n echo in\nfi; for f (a b); do :; done\necho end\n", 2, "first\n"},
		{"heredoc body inside a skip", "echo first\ncat <<A; if [[ ${(%):-x} ]]; then\nbody\nA\n echo hi\nfi\necho end\n", 2, "first\n"},
		{"repairs run out", "echo first\n" + strings.Repeat("echo $((1.5))\n", maxRepairs+1), 2, "first\n"},
	} {
		out, report := filterRC(tc.src, "zsh", "bash", func() *Report { return NewReport("", "") })
		if len(report.NotLoaded) != 1 {
			t.Errorf("%s: NotLoaded = %+v, want the one stop", tc.name, report.NotLoaded)
			continue
		}
		if n := report.NotLoaded[0]; n.Load != executor.LoadedBefore || n.Line != tc.from || n.Reason == "" {
			t.Errorf("%s: not loaded %+v, want from line %d with a reason", tc.name, n, tc.from)
		}
		if got := runFiltered(t, out); got != tc.ran {
			t.Errorf("%s: ran %q, want %q\n%s", tc.name, got, tc.ran, out)
		}
		if strings.Count(out, "\n") != strings.Count(tc.src, "\n") {
			t.Errorf("%s: the lines moved:\n%s", tc.name, out)
		}
	}
}

// A file whose first statement stops it loads nothing, and says so with
// the parser's line.
func TestFilterRC_AFileStoppedAtItsFirstStatementLoadsNothing(t *testing.T) {
	for _, src := range []string{
		"for f (a b); do echo $f; done\necho end\n",
		"$A[0[0[0 .", // the zsh parser takes it, but ends the word past the end of the file
	} {
		out, report := filterRC(src, "zsh", "bash", func() *Report { return NewReport("", "") })
		if len(report.NotLoaded) != 1 || report.NotLoaded[0].Load != executor.LoadedNone || report.NotLoaded[0].Reason == "" {
			t.Errorf("%q: NotLoaded = %+v, want nothing loaded, with a reason", src, report.NotLoaded)
		}
		if got := runFiltered(t, out); got != "" {
			t.Errorf("%q: ran %q, want nothing\n%s", src, got, out)
		}
	}
}

// A stop at the file's last statement keeps the lines before it and never
// reads past the last line (fuzz corpus 8c5bc31c1796519e: the parse error is
// on the last line, and the statements before it end on that line too).
func TestFilterRC_StopAtTheLastLine(t *testing.T) {
	newReport := func() *Report { return NewReport("", "") }
	for _, src := range []string{"\"\n\"<<A", "\"\n\"<<A\n", "echo 'a\nb'<<A", "\"\\n\"<<A"} {
		for _, shell := range []string{"zsh", ""} {
			out, _ := filterRC(src, shell, "bash", newReport)
			if _, err := parseRC(out, "bash"); err != nil {
				t.Errorf("%q (%q): %v\n%s", src, shell, err, out)
			}
		}
	}
}

// A bash file (.profile, or hash's own .hashrc in bash mode) is parsed as
// bash: bash syntax zsh's parser rejects stays, the skip rules still apply,
// and a file nothing is skipped in comes back as written.
func TestFilterRC_BashFilesUseTheBashTree(t *testing.T) {
	newReport := func() *Report { return NewReport("", "") }
	src := "case $x in a) echo a ;;& *) echo b ;; esac\nsetopt autocd\nexport A=1\n"
	out, report := filterRC(src, "bash", "bash", newReport)
	if len(report.NotLoaded) > 0 || out != "case $x in a) echo a ;;& *) echo b ;; esac\n: # [hash-compat] setopt autocd\nexport A=1\n" {
		t.Errorf("out %q, not loaded %+v", out, report.NotLoaded)
	}
	if report.Summary.Exports != 1 || report.Summary.Skipped != 1 {
		t.Errorf("summary %+v", report.Summary)
	}
	clean := "f() { echo \"$1\"; }\nalias ll='ls -l'\n"
	for _, shell := range []string{"bash", ""} {
		if out, _ := filterRC(clean, shell, "bash", newReport); out != clean {
			t.Errorf("%q: out %q, want it as written", shell, out)
		}
	}
}

// A skip that would cut a heredoc body out from under its operator only
// stops the file when it is made: a skip undone because
// the statement around it, operator and all, is skipped whole cuts nothing.
func TestFilterRC_UndoneSkipCutsNoHeredoc(t *testing.T) {
	src := "while cat <<EOF; if [[ ${(%):-x} ]]; then\nbody\nEOF\n  echo in\nfi; do break; done\necho end\n"
	out, report := filterRC(src, "zsh", "bash", func() *Report { return NewReport("", "") })
	if len(report.NotLoaded) > 0 {
		t.Fatalf("not loaded: %+v\n%s", report.NotLoaded, out)
	}
	if got := runFiltered(t, out); got != "end\n" {
		t.Errorf("output %q\n%s", got, out)
	}
}

// A skipped statement is ":", which succeeds: where it decides what runs
// next from inside another statement (the right of && || |, the last
// command of a { } or ( ) or of a branch, a $( ) in a word), that statement's
// status is unknown. A test whose status is unknown decides nothing: the
// compound it picks the branch of is skipped, and after && || | the rest is.
func TestFilterRC_SkipInsideATestDecidesNothing(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"if a && skipped", "if true && (( $+_comps[nonexistent_xyz] )); then\n  echo THEN\nelse\n  echo ELSE\nfi\n", ""},
		{"a && skipped && b", "true && [[ -z ${(%):-x} ]] && echo B\n", ""},
		{"a runs, b does not", "echo A && [[ -z ${(%):-x} ]] && echo B\n", "A\n"},
		{"three, skip last", "echo A && echo B && [[ -z ${(%):-x} ]] || echo C\n", "A\nB\n"},
		{"{ skipped } && b", "{ [[ -z ${(%):-x} ]] } && echo B\n", ""},
		{"{ a; skipped } || b", "{ echo A; [[ -n ${(%):-x} ]]; } || echo B\n", "A\n"},
		{"if ( skipped )", "if ( [[ -z ${(%):-x} ]] ); then echo S; fi\n", ""},
		{"while a && skipped", "while true && [[ -z ${(%):-x} ]]; do echo LOOP; break; done\n", ""},
		{"case $(skipped)", "case $(echo ${(%):-x}) in x) echo X ;; *) echo STAR ;; esac\n", ""},
		{"for in $(skipped)", "for v in $(echo ${(%):-x}); do echo V; done\n", ""},
		{"[[ $(skipped) ]] && b", "[[ -n \"$(echo ${(%):-x})\" ]] && echo B\n", ""},
		{"if [[ $(skipped) ]]", "if [[ -n \"$(setopt)\" ]]; then echo THEN; else echo ELSE; fi\n", ""},
		{"skipped last in then", "if true; then [[ -n ${(%):-x} ]]; fi && echo B\n", ""},
		{"skipped last in an arm", "case a in a) [[ -n ${(%):-x} ]] ;; esac && echo B\n", ""},
		{"skipped last in a loop", "for v in a; do [[ -n ${(%):-x} ]]; done && echo B\n", ""},
		{"elif dropped", "if false; then :; elif [[ -n ${(%):-x} ]]; then :; fi && echo B\n", ""},
		{"skipped right of a pipe", "echo A | [[ -n ${(%):-x} ]] && echo B\n", ""},
		{"x=$(skipped) at list position", "x=$(echo ${(%):-x}); echo kept\n", "kept\n"},
		{"skipped first in a block", "{ setopt x; true; } && echo B\n", "B\n"},
	} {
		out, _ := filterBash(t, tc.src+"echo end\n")
		if got := runFiltered(t, out); got != tc.want+"end\n" {
			t.Errorf("%s: output %q, want %q\nfiltered:\n%s", tc.name, got, tc.want+"end\n", out)
		}
	}
}

// A break or continue in a skipped statement (or dropped branch) never runs:
// the loop it would have left could run forever, or run what it would have
// jumped over, so the loop is skipped whole.
func TestFilterRC_SkippedBreakSkipsItsLoop(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"skip && break", "while true; do\n  [[ -n ${(%):-x} ]] && break\ndone\n", ""},
		{"break in a skipped if", "while true; do if [[ -n ${(%):-x} ]]; then break; fi; done\n", ""},
		{"break in a dropped elif", "while true; do if false; then :; elif [[ -n ${(%):-x} ]]; then break; fi; done\n", ""},
		{"break in a dropped arm", "while true; do case a in ${(%):-x}) break ;; esac; done\n", ""},
		{"continue", "for i in 1 2; do [[ -n ${(%):-x} ]] && continue; echo $i; done\n", ""},
		{"break 2", "for i in 1; do echo A; while true; do [[ -n ${(%):-x} ]] && break 2; done; done\n", ""},
		{"break of a loop inside the skip", "while true; do echo A; setopt x && while true; do break; done; break; done\n", "A\n"},
		{"kept break", "until false; do setopt x; break; done\n", ""},
		{"break in a skipped function", "while true; do echo A; function a b { break }; break; done\n", "A\n"},
	} {
		out, _ := filterBash(t, tc.src+"echo end\n")
		if got := runFiltered(t, out); got != tc.want+"end\n" {
			t.Errorf("%s: output %q, want %q\nfiltered:\n%s", tc.name, got, tc.want+"end\n", out)
		}
	}
}

// zsh's disown separators &! and &| are & for bash (hash has no job table
// to disown from), so a file that uses them stays on the syntax tree.
func TestFilterRC_DisownIsBackground(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"{ zcompile ~/.zcompdump } &!\nif true; then\n  echo b\nfi\n", "{ zcompile ~/.zcompdump; } &\nif true; then\n  echo b\nfi\n"},
		{"true &|\necho after\n", "true &\necho after\n"},
		{"setopt x &!\necho after\n", ": &\necho after\n"},
	} {
		out, report := filterRC(tc.src, "zsh", "bash", func() *Report { return NewReport("", "") })
		if len(report.NotLoaded) > 0 || out != tc.want {
			t.Errorf("%q: out %q, want %q; not loaded %+v", tc.src, out, tc.want, report.NotLoaded)
		}
	}
}

// A parse error past the column the parser's positions can hold (16383)
// still names its statement, by its offset: only that statement is skipped.
func TestFilterRC_ParseErrorOnALongLine(t *testing.T) {
	src := "x=\"" + strings.Repeat("a", 17000) + "\"; echo $((1.5)); echo kept\n"
	out, report := filterRC(src, "zsh", "bash", func() *Report { return NewReport("", "") })
	if len(report.NotLoaded) > 0 || runFiltered(t, out) != "kept\n" {
		t.Errorf("not loaded %+v; output %q", report.NotLoaded, runFiltered(t, out))
	}
}

// An arm the arm before it falls through into (;&) runs after that arm
// whatever its pattern, so a skipped pattern takes only the pattern: the
// arms after the fallthrough chain still depend on it, and are dropped.
func TestFilterRC_FallthroughIntoASkippedPattern(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"one line", "case a in a) echo A ;& ${(%):-x}) echo FALL ;; esac\n", "A\nFALL\n"},
		{"lines", "case a in\n  a)\n    echo A\n    ;&\n  ${(%):-x})\n    echo FALL\n    ;;\nesac\n", "A\nFALL\n"},
		{"op on the arm's line", "case a in\n  a) echo A ;&\n  ${(%):-x}) echo FALL ;;\nesac\n", "A\nFALL\n"},
		{"empty arm", "case a in a) ;& ${(%):-x}) echo FALL ;; esac\n", "FALL\n"},
		{"empty skipped arm", "case a in a) echo A ;& ${(%):-x}) ;; esac\n", "A\n"},
		{"chain goes on", "case a in a) echo A ;& ${(%):-x}) echo FALL ;& b) echo B ;; *) echo STAR ;; esac\n", "A\nFALL\nB\n"},
		{"arms after the chain", "case b in a) echo A ;& ${(%):-x}) echo FALL ;; *) echo STAR ;; esac\n", ""},
		{"no fallthrough", "case a in a) echo A ;; ${(%):-x}) echo X ;; *) echo STAR ;; esac\n", "A\n"},
	} {
		out, _ := filterBash(t, tc.src+"echo end\n")
		if got := runFiltered(t, out); got != tc.want+"end\n" {
			t.Errorf("%s: output %q, want %q\nfiltered:\n%s", tc.name, got, tc.want+"end\n", out)
		}
	}
}

// zsh code bash parses too, but reads differently, is skipped, or put in
// what hash runs: 1-based array indexes, the tied arrays (fpath, cdpath,
// manpath; path when it is not a plain list), zsh's print and emulate, and
// the clobbering redirections >! >| >>! &>!.
func TestFilterRC_ZshCodeBashReadsDifferently(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"${a[1]}", "a=(x y z)\necho ${a[1]}\n", ""},
		{"${a[2,3]}", "a=(x y z)\necho ${a[2,3]}\n", ""},
		{"${a[-1]} is the same", "a=(x y z)\necho ${a[-1]}\n", "z\n"},
		{"a[1]=", "a=(x y z)\na[1]=w\necho ${a[@]}\n", "x y z\n"},
		{"print", "print -r hi\n", ""},
		{"emulate", "emulate -L zsh\n", ""},
		{"fpath", "fpath=(/x $fpath)\necho ${fpath[@]}\n", "\n"},
		{"cdpath", "cdpath=(/x)\necho ${cdpath[@]}\n", "\n"},
		{"path with a glob", "path=(/x/*(N) $path)\necho ${path[@]}\n", "\n"},
		{"path[1]", "path[1]=/x\necho ${path[@]}\n", "\n"},
		{"path and more", "path=(/a) X=1\necho ${path[@]}\n", "\n"},
		{"path with an empty-able variable", "path=($X /a)\necho ${path[@]}\n", "\n"},
	} {
		out, _ := filterBash(t, tc.src)
		if got := runFiltered(t, out); got != tc.want {
			t.Errorf("%s: output %q, want %q\nfiltered:\n%s", tc.name, got, tc.want, out)
		}
	}
	for src, want := range map[string]string{
		"echo x >! f\n":       "echo x > f\n",
		"echo x 2>! f\n":      "echo x 2> f\n",
		"echo x >>! f\n":      "echo x >> f\n",
		"echo x >>| f\n":      "echo x >> f\n",
		"echo x &>! f\n":      "echo x &> f\n",
		"echo x &>>| f\n":     "echo x &>> f\n",
		"echo x >| f\n":       "echo x > f\n",
		"setopt x >! f\n":     ": # [hash-compat] setopt x >! f\n",
		"echo $(echo >! f)\n": "echo $(echo > f)\n",
	} {
		if out, _ := filterBash(t, src); out != want {
			t.Errorf("%q: %q, want %q", src, out, want)
		}
	}
	// the interpreter has no noclobber, and no >| either
	f := filepath.Join(t.TempDir(), "f")
	out, _ := filterBash(t, "echo x >! "+f+"\necho y >>! "+f+"\ncat "+f+"\n")
	if got := runFiltered(t, out); got != "x\ny\n" {
		t.Errorf("clobbering redirections: output %q\n%s", got, out)
	}
}

// path=(...) is PATH as a list in zsh; when the list is plain (words,
// quoted strings, $path, upper-case variables), it becomes the export of
// PATH that zsh makes of it.
func TestFilterRC_PathListIsPATH(t *testing.T) {
	for src, want := range map[string]string{
		"path=(/opt/x/bin $path)": "/opt/x/bin:/usr/bin:/bin\n",
		"path+=(/opt/y/bin)":      "/usr/bin:/bin:/opt/y/bin\n",
		"path=(\n  ~/bin\n  \"$HOME/a b\"\n  '/q'\n  $path\n  ${path[@]}\n  \"${path[@]}\"\n  $HOME/.local/bin\n)": "/h/bin:/h/a b:/q:/usr/bin:/bin:/usr/bin:/bin:/usr/bin:/bin:/h/.local/bin\n",
	} {
		out, report := filterBash(t, src+"\necho \"$PATH\"\n")
		if got := runPATH(t, out, "hash"); got != want || report.Summary.Exports != 1 {
			t.Errorf("%q: PATH %q, want %q; %d exports\nfiltered:\n%s", src, got, want, report.Summary.Exports, out)
		}
		if zsh := runPATH(t, src+"\necho \"$PATH\"\n", "zsh"); zsh != "" && zsh != want {
			t.Errorf("%q: zsh PATH %q, want %q", src, zsh, want)
		}
	}
}

// runPATH runs src with HOME=/h and PATH=/usr/bin:/bin, in the interpreter
// (shell "hash") or in real zsh, and returns its output, or "" when there
// is no zsh.
func runPATH(t *testing.T, src, shell string) string {
	t.Helper()
	if shell == "zsh" {
		if _, err := os.Stat(zshPath); err != nil {
			return ""
		}
		cmd := exec.Command(zshPath, "-f", "-c", src)
		cmd.Env = []string{"HOME=/h", "PATH=/usr/bin:/bin"}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("zsh: %v", err)
		}
		return string(out)
	}
	prog, err := parseRC(src, "bash")
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	r, err := interp.New(interp.StdIO(nil, &out, &out), interp.Env(expand.ListEnviron("HOME=/h", "PATH=/usr/bin:/bin")))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Run(context.Background(), prog); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// A skipped statement holding a return or exit (`[[ zsh-only ]] && return`)
// may be where zsh stops reading the file, and what comes after it may be
// what the guard kept from running: the rest of the file is commented out
// there, and reported.
func TestFilterRC_SkippedReturnStopsTheFile(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"guard", "echo a\n[[ -n ${(%):-x} ]] && return\necho b\nalias x=y\n", "a\n"},
		{"exit, then ;", "echo a\n[[ -n ${(%):-x} ]] && exit 0; echo b\necho c\n", "a\n"},
		{"in an if", "if true; then\n  [[ -n ${(%):-x} ]] && return\nfi\necho b\n", ""}, // returns there
		{"in a function", "f() { [[ -n ${(%):-x} ]] && return; echo f; }\necho b\nf\n", "b\n"},
		{"in a subshell", "( [[ -n ${(%):-x} ]] && exit )\necho b\n", "b\n"},
		{"return kept", "true || return\necho b\n", "b\n"},
		{"last statement", "echo a\n[[ -n ${(%):-x} ]] && return\n", "a\n"},
	} {
		out, report := filterBash(t, tc.src)
		if got := runFiltered(t, out); got != tc.want {
			t.Errorf("%s: output %q, want %q\nfiltered:\n%s", tc.name, got, tc.want, out)
		}
		stopped := slices.ContainsFunc(report.SkippedItems, func(s SkippedItem) bool { return strings.HasPrefix(s.Reason, "stopped") })
		if wantStop := !slices.Contains([]string{"in a subshell", "return kept", "last statement", "in an if"}, tc.name); stopped != wantStop {
			t.Errorf("%s: stopped %v, want %v: %+v", tc.name, stopped, wantStop, report.SkippedItems)
		}
		if tc.name == "guard" && report.Summary.Aliases != 0 {
			t.Errorf("guard: the alias after the stop counts: %+v", report.Summary)
		}
	}
}

// A statement in a $( ) of a heredoc body is code, and is skipped there
// like anywhere else: only a skip that would cut a body out from under its
// operator stops the file.
func TestFilterRC_SkipInsideAHeredocBody(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"cat <<EOF\n$(setopt x; echo inbody)\nEOF\necho end\n", "inbody\nend\n"},
		{"cat <<EOF\nline $(\n  print -P x\n  echo in\n)\nEOF\necho end\n", "line in\nend\n"},
		{"cat <<-EOF\n\tA$(setopt x)\n\tEOF\necho end\n", "A\nend\n"},
	} {
		out, report := filterRC(tc.src, "zsh", "bash", func() *Report { return NewReport("", "") })
		if len(report.NotLoaded) > 0 {
			t.Errorf("%q: not loaded %+v", tc.src, report.NotLoaded)
			continue
		}
		if got := runFiltered(t, out); got != tc.want {
			t.Errorf("%q: output %q, want %q\n%s", tc.src, got, tc.want, out)
		}
	}
}

// A function whose last command is skipped (or not kept) returns a status
// zsh would not give it: a call to it decides nothing, like the skip itself.
func TestFilterRC_FunctionWhoseStatusIsUnknownDecidesNothing(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"if", "_has() { (( $+_comps[$1] )) }\nif _has no_such_cmd; then echo GUARDED; fi\necho end\n", "end\n"},
		{"&&", "f() { [[ -n ${(M)x:#y} ]]; }\nf && echo GUARDED\necho end\n", "end\n"},
		{"negated", "f() { true; [[ -n ${(%):-x} ]]; }\nif ! f; then :; else echo GUARDED; fi\necho end\n", "end\n"},
		{"function keyword", "function f { setopt autocd }\nf && echo GUARDED\necho end\n", "end\n"},
		{"through another function", "f() { [[ -n ${(%):-x} ]] }\ng() { f }\ng && echo GUARDED\necho end\n", "end\n"},
		{"while", "f() { [[ -n ${(%):-x} ]] }\nwhile f; do echo GUARDED; break; done\necho end\n", "end\n"},
		{"kept function decides", "f() { [[ -n ${(%):-x} ]]; true; }\nf && echo kept\n", "kept\n"},
		{"called at list position", "f() { echo in; [[ -n ${(%):-x} ]] }\nf\necho end\n", "in\nend\n"},
		{"redefined kept", "f() { [[ -n ${(%):-x} ]] }\nf() { true; }\nf && echo kept\n", "kept\n"},
	} {
		out, report := filterBash(t, tc.src)
		if len(report.NotLoaded) > 0 {
			t.Errorf("%s: not loaded %+v", tc.name, report.NotLoaded)
		}
		if got := runFiltered(t, out); got != tc.want {
			t.Errorf("%s: output %q, want %q\nfiltered:\n%s", tc.name, got, tc.want, out)
		}
	}
}

// A skipped guard that may return from a function stops the function there,
// as the file at the top level: the rest of its body may be what the guard
// kept from running. Its status is then unknown. The outputs are zsh -f's.
func TestFilterRC_SkippedReturnStopsTheFunction(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"guard", "f() { [[ -n ${(%):-x} ]] && return; echo f; }\necho b\nf\n", "b\n"},
		{"lines", "setup() {\n  (( $+_comps[fzf] )) || return\n  alias ff='fzf'\n  echo set\n}\nsetup\necho after\n", "after\n"},
		{"its status", "f() { [[ -n ${(%):-x} ]] && return 1; return 0 }\nf && echo GUARDED\necho end\n", "end\n"},
		{"subshell body", "f() ( [[ -n ${(%):-x} ]] && exit; echo f )\nf; echo end\n", "end\n"},
		{"in an if", "f() { echo a; if true; then [[ -n ${(%):-x} ]] && return; fi; echo f; }\nf; echo end\n", "a\nend\n"},
		{"in a ( ) stops nothing", "f() { ( [[ -n ${(%):-x} ]] && return ); echo f; }\nf; echo end\n", "f\nend\n"},
		{"} on the last line", "f() {\n  [[ -n ${(%):-x} ]] && return\n  echo f }\nf; echo end\n", "end\n"},
		{"heredoc last", "f() {\n  [[ -n ${(%):-x} ]] && return\n  cat <<EOF\nbody\nEOF\n}\nf; echo end\n", "end\n"},
		{"heredoc in a $( )", "f() {\n  [[ -n ${(%):-x} ]] && return\n  x=$(cat <<EOF\nbody\nEOF\n)\n  echo $x }\nf; echo end\n", "end\n"},
		{"nested function", "f() {\n  g() { [[ -n ${(%):-x} ]] && return; echo g; }\n  echo f\n}\nf; g; echo end\n", "f\nend\n"},
	} {
		out, report := filterBash(t, tc.src)
		if len(report.NotLoaded) > 0 {
			t.Errorf("%s: not loaded %+v", tc.name, report.NotLoaded)
		}
		if got := runFiltered(t, out); got != tc.want {
			t.Errorf("%s: output %q, want %q\nfiltered:\n%s", tc.name, got, tc.want, out)
		}
	}
	_, report := filterBash(t, "setup() {\n  (( $+_comps[fzf] )) || return\n  alias ff='fzf'\n}\nsetup\n")
	if report.Summary.Aliases != 0 {
		t.Errorf("the alias after the stop counts: %+v", report.Summary)
	}
}

// A negated command is skipped by the same rules as the command: "! " is no
// command name. ":" succeeds, so the whole statement goes, "!" too.
func TestFilterRC_NegatedCommandIsSkippedByTheRules(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"no-op builtin in an if", "if ! zmodload zsh/datetime 2>/dev/null; then echo GUARDED; fi\necho end\n", "end\n"},
		{"in a list", "! setopt autocd && echo GUARDED\necho end\n", "end\n"},
		{"autoload", "! autoload -Uz compinit || echo GUARDED\necho end\n", "end\n"},
		{"global alias", "! alias -g G=x && echo GUARDED\necho end\n", "end\n"},
		{"typeset flag", "! typeset -U path || echo GUARDED\necho end\n", "end\n"},
		{"init line", "! eval \"$(starship init zsh)\" || echo GUARDED\necho end\n", "end\n"},
		{"kept", "! false && echo kept\n", "kept\n"},
	} {
		out, report := filterBash(t, tc.src)
		if len(report.NotLoaded) > 0 {
			t.Errorf("%s: not loaded %+v", tc.name, report.NotLoaded)
		}
		if got := runFiltered(t, out); got != tc.want {
			t.Errorf("%s: output %q, want %q\nfiltered:\n%s", tc.name, got, tc.want, out)
		}
	}
}

// zsh's ;| goes on testing the patterns after its arm, as bash's ;;& does.
// The outputs are zsh -f's.
func TestFilterRC_ResumeCaseIsBashs(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"lines", "case a in\n  a) echo m1 ;|\n  *) echo m2 ;;\n  *) echo m3 ;;\nesac\n", "m1\nm2\n"},
		{"one line", "case ab in (a*) echo A;| (*b) echo B;; esac\n", "A\nB\n"},
		{"skipped body", "case a in\n  a) setopt autocd ;|\n  a) echo m2 ;;\nesac\n", "m2\n"},
		{"last arm", "case a in\n  a) echo m1 ;|\nesac\necho end\n", "m1\nend\n"},
		{"after a merged ;& chain", "case a in\n  a) echo one ;&\n  ${(%):-x}) echo two ;|\n  *) echo three ;;\nesac\n", "one\ntwo\nthree\n"},
		{"arms after the chain's ;|", "case a in\n  a) echo one ;&\n  ${(%):-x}) echo two ;|\n  b) echo b ;;\n  *) echo star ;;\nesac\n", "one\ntwo\nstar\n"},
	} {
		out, report := filterBash(t, tc.src)
		if len(report.NotLoaded) > 0 {
			t.Errorf("%s: not loaded %+v", tc.name, report.NotLoaded)
		}
		if got := runFiltered(t, out); got != tc.want {
			t.Errorf("%s: output %q, want %q\nfiltered:\n%s", tc.name, got, tc.want, out)
		}
	}
}

// exec guards nothing that follows it: when zsh does exec (tmux, a login
// zsh), the new shell reads the rc file again, so a skipped guard holding
// exec stops neither the file nor a function, and what follows it runs.
func TestFilterRC_SkippedExecStopsNothing(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"guard", "echo a\n[[ -n ${(%):-x} ]] && exec zsh -l\necho b\n", "a\nb\n"},
		{"exec tmux in an if", "if [[ -z \"$TMUX\" ]] && [[ -n ${(%):-x} ]]; then\n  exec tmux\nfi\nexport EDITOR=vim\nalias ll='ls -l'\necho b\n", "b\n"},
		{"through a function", "has() { [[ -n ${(%):-x} ]]; }\n[[ -z $TMUX ]] && has tmux && exec tmux\nalias g=git\necho b\n", "b\n"},
		{"in a function", "f() { [[ -n ${(%):-x} ]] && exec tmux; echo f; }\nf; echo end\n", "f\nend\n"},
		{"redirections alone", "echo a\n[[ -n ${(%):-x} ]] && exec 2>/dev/null\necho b\n", "a\nb\n"},
		{"fd variable", "echo a\n[[ -n ${(%):-x} ]] && exec {fd}>/dev/null\necho b\n", "a\nb\n"},
	} {
		out, report := filterBash(t, tc.src)
		if len(report.NotLoaded) > 0 {
			t.Errorf("%s: not loaded %+v", tc.name, report.NotLoaded)
		}
		if got := runFiltered(t, out); got != tc.want {
			t.Errorf("%s: output %q, want %q\nfiltered:\n%s", tc.name, got, tc.want, out)
		}
		if strings.Contains(tc.src, "alias") && report.Summary.Aliases != 1 {
			t.Errorf("%s: the alias after the exec is not counted: %+v", tc.name, report.SkippedItems)
		}
	}
}

// A function whose body is skipped whole (a zsh-only redirection on it)
// defines nothing to stop: a return in the body is under the body's skip,
// not a guard of its own, and stopping after it cut into that skip.
func TestFilterRC_FunctionBodySkippedWholeStopsNothing(t *testing.T) {
	for _, src := range []string{
		"f() {\n  echo a\n  return 0\n  echo b\n}  2>${(%):-/dev/null}\nalias g=git\necho end\n",
		"f() {\n  [[ -n $1 ]] || return\n  echo b\n} >>*(N)\necho end\n",
	} {
		out, report := filterBash(t, src)
		if len(report.NotLoaded) > 0 {
			t.Errorf("%q: not loaded %+v", src, report.NotLoaded)
		}
		if got := runFiltered(t, out); got != "end\n" {
			t.Errorf("%q: output %q, want %q\nfiltered:\n%s", src, got, "end\n", out)
		}
	}
}

// A statement skipped whole defines nothing: a function it defined (or
// redefined) in a part walked before the skip was decided keeps the status
// it had before. The outputs are zsh -f's for the kept markers.
func TestFilterRC_SkippedStatementDefinesNoFunction(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"kept redefinition in a skipped loop", "f() { [[ $TERM == (#i)dumb ]]; }\nwhile true; do\n  f() { false; }\n  [[ -n ${(%):-x} ]] && break\ndone\nf && echo GUARDED\necho end\n", "end\n"},
		{"unknown redefinition in a skipped loop", "f() { true; }\nwhile true; do\n  f() { setopt autocd; }\n  [[ -n ${(%):-x} ]] && break\ndone\nf && echo kept\n", "kept\n"},
		{"in an if skipped whole", "f() { true; }\nif true; then\n  f() { setopt autocd; }\nelif [[ -n ${(%):-x} ]]; then cat <<EOF; fi\nbody\nEOF\nf && echo kept\n", "kept\n"},
	} {
		out, report := filterBash(t, tc.src)
		if len(report.NotLoaded) > 0 {
			t.Errorf("%s: not loaded %+v", tc.name, report.NotLoaded)
		}
		if got := runFiltered(t, out); got != tc.want {
			t.Errorf("%s: output %q, want %q\nfiltered:\n%s", tc.name, got, tc.want, out)
		}
	}
}

// A skipped return guard nested in a compound returns there: nothing after
// it runs, inside the compound (the rest of its { } or branch, the branches
// or arms after it) or after it. The outputs are zsh -f's, where the guard
// returns.
func TestFilterRC_NestedReturnGuardReturnsThere(t *testing.T) {
	const g = "[[ -n ${(%):-x} ]] && return"
	for _, tc := range []struct{ name, src, want string }{
		{"then body", "if true; then\n  echo a\n  " + g + "\n  echo b\nfi\necho c\n", "a\n"},
		{"block", "{ echo a; " + g + "; echo b; }\necho c\n", "a\n"},
		{"else body", "if false; then :; else echo a; " + g + "; echo b; fi; echo c\n", "a\n"},
		{"else after the then body", "if true; then echo a; " + g + "; echo b; else echo e; fi; echo c\n", "a\n"},
		{"elif after the then body", "if true; then echo a; " + g + "; elif true; then echo e; fi; echo c\n", "a\n"},
		{"arm falling through", "case a in a) echo a; " + g + "; echo b ;& b) echo fall ;; esac; echo c\n", "a\n"},
		{"arm resuming", "case a in a) echo a; " + g + "; echo b ;| *) echo star ;; esac; echo c\n", "a\n"},
		{"arm on lines", "case a in\n  a)\n    echo a\n    " + g + "\n    echo b\n    ;&\n  b) echo fall ;;\nesac\necho c\n", "a\n"},
		{"right of &&", "true && { echo a; " + g + "; echo b; }; echo c\n", "a\n"},
		{"left of &&", "{ echo a; " + g + "; echo b; } && echo y; echo c\n", "a\n"},
		{"in a function", "f() { if true; then echo a; " + g + "; echo x; fi; echo y; }; f; echo end\n", "a\nend\n"},
		{"heredoc after it", "if true; then\n  echo a\n  " + g + "\n  cat <<EOF\nbody\nEOF\nfi\necho c\n", "a\n"},
		{"in a test", "if { echo a; " + g + "; true; }; then echo b; fi; echo c\n", "a\n"},
		{"in a loop", "for v in a b; do echo $v; " + g + "; echo x; done; echo c\n", "a\n"},
		{"in a while loop", "while true; do echo a; " + g + "; echo x; done; echo c\n", "a\n"},
	} {
		out, report := filterBash(t, tc.src)
		if len(report.NotLoaded) > 0 {
			t.Errorf("%s: not loaded %+v", tc.name, report.NotLoaded)
		}
		if got := runFiltered(t, out); got != tc.want {
			t.Errorf("%s: output %q, want %q\nfiltered:\n%s", tc.name, got, tc.want, out)
		}
	}
}

// A skipped return guard nested in a compound returns only where it is
// reached: what follows it on the paths that do not reach it runs, as in
// zsh. The outputs are zsh -f's: where the guard is reached it returns.
// The guard's own statement becomes the return, a dropped elif an else
// that returns, dropped case arms an arm that does.
func TestFilterRC_NestedReturnGuardKeepsOtherPaths(t *testing.T) {
	const g = "[[ -n ${(%):-x} ]] && return"
	for _, tc := range []struct{ name, src, want, filtered string }{
		{"branch not taken", "if false; then\n  echo a\n  " + g + "\nfi\necho c\nalias k=kubectl\n", "c\n", "  return # [hash-compat] " + g},
		{"branch taken", "if true; then\n  echo a\n  " + g + "\nfi\necho c\n", "a\n", ""},
		{"other branch", "if false; then " + g + "; else echo e; fi; echo c\n", "e\nc\n", ""},
		{"in a function", "f() {\n  if [ \"$1\" = x ]; then " + g + "; fi\n  echo \"f $1\"\n}\nf x\nf y\necho end\n", "f y\nend\n", ""},
		{"arm not taken", "case b in a) " + g + " ;; b) echo b ;; esac; echo c\n", "b\nc\n", ""},
		{"right of && not taken", "false && { " + g + "; }; echo c\n", "c\n", ""},
		{"dropped elif", "if true; then echo a; elif [[ -n ${(%):-x} ]]; then return; fi; echo c\n", "a\nc\n", "if true; then echo a; else return; fi; echo c"},
		{"dropped elif reached", "if false; then echo a; elif [[ -n ${(%):-x} ]]; then return; fi; echo c\n", "", ""},
		{"dropped arms", "case a in\n  a) echo a ;;\n  ${(%):-b}) return ;;\nesac\necho c\n", "a\nc\n", "\n*) return;; esac"},
		{"dropped arms reached", "case z in\n  a) echo a ;;\n  ${(%):-z}) return ;;\nesac\necho c\n", "", ""},
	} {
		out, report := filterBash(t, tc.src)
		if len(report.NotLoaded) > 0 {
			t.Errorf("%s: not loaded %+v", tc.name, report.NotLoaded)
		}
		if got := runFiltered(t, out); got != tc.want {
			t.Errorf("%s: output %q, want %q\nfiltered:\n%s", tc.name, got, tc.want, out)
		}
		if tc.filtered != "" && !strings.Contains(out, tc.filtered) {
			t.Errorf("%s: filtered\n%s\nwant it to hold %q", tc.name, out, tc.filtered)
		}
		if slices.ContainsFunc(report.SkippedItems, func(s SkippedItem) bool { return strings.HasPrefix(s.Reason, "stopped") }) {
			t.Errorf("%s: stopped: %+v", tc.name, report.SkippedItems)
		}
		if strings.Contains(tc.src, "alias") && report.Summary.Aliases != 1 {
			t.Errorf("%s: the alias after the guard is not counted: %+v", tc.name, report.Summary)
		}
	}
}

// Where a runtime return is not what zsh's would be, the guard still stops
// what follows it: an exit guard (a return would not end the shell), a
// guard in a pipeline or a background job (bash runs it in a subshell,
// zsh runs a pipeline's last command in the shell), a skip holding an exit
// too, and any guard in the zsh dialect, where a file may run as a line
// (a return there ends nothing).
func TestFilterRC_GuardsThatStillStop(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"exit", "if true; then\n  echo a\n  [[ -n ${(%):-x} ]] && exit\n  echo b\nfi\necho c\n", "a\n"},
		{"exit, branch not taken", "if false; then [[ -n ${(%):-x} ]] && exit; fi; echo c\n", ""},
		{"exit in a loop", "for v in a b; do echo $v; [[ -n ${(%):-x} ]] && exit; echo x; done; echo c\n", ""},
		{"pipeline", "f() { true | { [[ -n ${(%):-x} ]] && return; }; echo f; }\nf\necho end\n", "end\n"},
		{"background", "f() { { [[ -n ${(%):-x} ]] && return; } & wait; echo f; }\nf\necho end\n", "end\n"},
		{"return and exit", "if true; then\n  [[ -n ${(%):-x} ]] && { return; exit; }\nfi\necho c\n", ""},
	} {
		out, report := filterBash(t, tc.src)
		if got := runFiltered(t, out); got != tc.want {
			t.Errorf("%s: output %q, want %q\nfiltered:\n%s", tc.name, got, tc.want, out)
		}
		if !slices.ContainsFunc(report.SkippedItems, func(s SkippedItem) bool { return strings.HasPrefix(s.Reason, "stopped") }) {
			t.Errorf("%s: not stopped: %+v\n%s", tc.name, report.SkippedItems, out)
		}
	}

	rcFile := filepath.Join(t.TempDir(), ".zshrc")
	if err := os.WriteFile(rcFile, []byte("if false; then\n  setopt autocd || return\nfi\necho c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, report, err := FilterWithDialect(rcFile, "zsh", "zsh")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "return #") || !slices.ContainsFunc(report.SkippedItems, func(s SkippedItem) bool { return strings.HasPrefix(s.Reason, "stopped") }) {
		t.Errorf("zsh dialect: filtered\n%s\nwant the guard to stop what follows", out)
	}
}

// exit in a function ends the shell: a skipped guard holding one stops the
// function body and makes the function one that may end the shell, so a
// call to it stops the file after the statement that holds the call, like
// a guard of its own, and so does a skip that defines one zsh may call.
// The outputs are zsh -f's, where the guard exits, but for a call in a test
// or a loop, skipped whole, and a definition skipped whole, which stops the
// file there.
func TestFilterRC_FunctionThatMayExitStopsTheFile(t *testing.T) {
	const g = "[[ -n ${(%):-x} ]] && exit"
	for _, tc := range []struct{ name, src, want, reason string }{
		{"call", "f() { " + g + "; echo f; }\necho a\nf\necho b\nalias g=git\n", "a\n", "a call to f could end the shell"},
		{"call runs", "f() { echo f; " + g + "; echo x; }\necho a\nf\necho b\n", "a\nf\n", "a call to f could end the shell"},
		{"defined in a skipped definition", "function a b {\n  f() { exit; }\n  f\n}\necho b\n", "", "a skipped guard could end the shell"},
		{"through another function", "f() { " + g + "; }\ng() { f; echo g; }\necho a\ng\necho b\n", "a\n", "a call to g could end the shell"},
		{"skipped call", "f() { " + g + "; }\nsetopt autocd && f\necho b\n", "", "a call to f could end the shell"},
		{"in a test", "f() { echo f; " + g + "; }\nif f; then echo t; fi\necho b\n", "", "a call to f could end the shell"},
		{"body skipped whole", "f() {\n  exit 1\n} 2>${(%):-/dev/null}\necho a\nf\necho b\n", "a\n", "a call to f could end the shell"},
		{"in a loop", "f() { " + g + "; }\nfor v in a b; do echo $v; f; done\necho b\n", "", "a call to f could end the shell"},
		{"stop text in the function", "f() { " + g + "; echo f; }\n", "", "a skipped guard could end the shell"},
		{"in a $( )", "f() { " + g + "; echo f; }\nx=$(f)\necho b\n", "b\n", ""},
		{"subshell body", "f() ( " + g + "; echo f )\nf\necho b\n", "b\n", ""},
		{"return", "f() { [[ -n ${(%):-x} ]] && return; echo f; }\nf\necho b\n", "b\n", ""},
		{"kept exit", "f() { echo f; exit; }\necho a\n", "a\n", ""},
		{"defined and called in a skip", "if [[ -n ${(%):-x} ]]; then\n  f() { exit; }\n  f\nfi\necho b\n", "", "a skipped guard could end the shell"},
		{"definition skipped whole", "function a b { exit 1; }\necho a\n", "", "a skipped guard could end the shell"},
		{"called before", "f 2>/dev/null\nf() { " + g + "; }\necho b\n", "b\n", ""},
	} {
		out, report := filterBash(t, tc.src)
		if len(report.NotLoaded) > 0 {
			t.Errorf("%s: not loaded %+v", tc.name, report.NotLoaded)
		}
		if got := runFiltered(t, out); got != tc.want {
			t.Errorf("%s: output %q, want %q\nfiltered:\n%s", tc.name, got, tc.want, out)
		}
		stopped := slices.ContainsFunc(report.SkippedItems, func(s SkippedItem) bool {
			return tc.reason != "" && s.Reason == "stopped: "+tc.reason+" here"
		})
		if tc.reason != "" && !stopped {
			t.Errorf("%s: no stop %q: %+v", tc.name, tc.reason, report.SkippedItems)
		}
		if strings.Contains(tc.src, "alias") && report.Summary.Aliases != 0 {
			t.Errorf("%s: the alias after the stop counts", tc.name)
		}
	}
}

// A return with no status, or with $? or a variable, returns the status of
// the statement before it, and a command that reads $? reads it: when that
// statement is not kept, neither is the return's (or the read's) status.
// The outputs are zsh -f's.
func TestFilterRC_ReturnOfAnUnknownStatusIsUnknown(t *testing.T) {
	const has = "has() { [[ -z ${(%):-x} ]]; }\n"
	for _, tc := range []struct{ name, src, want string }{
		{"return $?", has + "f() { has \"$1\"; return $?; }\nf x && echo GUARDED\necho end\n", "end\n"},
		{"local r=$?", has + "g() { has \"$1\"; local r=$?; return $r; }\ng x && echo GUARDED\necho end\n", "end\n"},
		{"bare return", has + "f() { has; return; }\nf && echo GUARDED\necho end\n", "end\n"},
		{"test of $?", has + "has\n[[ $? -eq 0 ]] && echo GUARDED\necho end\n", "end\n"},
		{"if test of $?", has + "has\nif [[ $? -eq 0 ]]; then echo GUARDED; fi\necho end\n", "end\n"},
		{"$? after a kept command", has + "has\ntrue\n[[ $? -eq 0 ]] && echo kept\n", "kept\n"},
		{"return 1", has + "f() { has; return 1; }\nf || echo kept\n", "kept\n"},
		{"return $? of a kept command", "f() { false; return $?; }\nf || echo kept\n", "kept\n"},
	} {
		out, report := filterBash(t, tc.src)
		if len(report.NotLoaded) > 0 {
			t.Errorf("%s: not loaded %+v", tc.name, report.NotLoaded)
		}
		if got := runFiltered(t, out); got != tc.want {
			t.Errorf("%s: output %q, want %q\nfiltered:\n%s", tc.name, got, tc.want, out)
		}
	}
}

// The first statement of a { } or ( ) body, of a case arm or a loop body,
// and the right of a |, read the $? of the statement before the compound;
// a case word or for list that reads it picks by it, so the compound is
// skipped whole. The outputs are zsh -f's, but for those skips.
func TestFilterRC_StatusReachesTheFirstStatementOfABody(t *testing.T) {
	const skipped = "[[ -z ${(%):-x} ]]\n" // fails in zsh, ":" in bash
	for _, tc := range []struct{ name, src, want string }{
		{"brace return", "f() {\n  " + skipped + "  { return; }\n}\nf && echo GUARDED\necho end\n", "end\n"},
		{"subshell exit $?", "g() {\n  " + skipped + "  ( exit $? )\n}\ng && echo GUARDED\necho end\n", "end\n"},
		{"brace reads $?", skipped + "{ [ $? -eq 0 ] && echo GUARDED; }\necho end\n", "end\n"},
		{"subshell reads $?", skipped + "( [ $? -eq 0 ] && echo GUARDED )\necho end\n", "end\n"},
		{"case word", skipped + "case $? in 0) echo GUARDED;; esac\necho end\n", "end\n"},
		{"case arm", skipped + "case x in x) [ $? -eq 0 ] && echo GUARDED;; esac\necho end\n", "end\n"},
		{"for body", skipped + "for i in a; do [ $? -eq 0 ] && echo GUARDED; done\necho end\n", "end\n"},
		{"right of a pipe", skipped + "true | [ $? -eq 0 ] && echo GUARDED\necho end\n", "end\n"},
		{"kept before a brace", "false\n{ [ $? -eq 1 ] && echo kept; }\n", "kept\n"},
		{"kept before a case word", "false\ncase $? in 1) echo kept;; esac\n", "kept\n"},
		{"then body reads the test", skipped + "if true; then [ $? -eq 0 ] && echo kept; fi\n", "kept\n"},
		{"right of && reads the left", skipped + "true && [ $? -eq 0 ] && echo kept\n", "kept\n"},
		{"arm after ;& reads the arm before", skipped + "case x in x) true;& y) [ $? -eq 0 ] && echo kept;; esac\n", "kept\n"},
		{"for list reads $?", skipped + "for i in $?; do echo GUARDED $i; done\necho end\n", "end\n"},
	} {
		out, report := filterBash(t, tc.src)
		if len(report.NotLoaded) > 0 {
			t.Errorf("%s: not loaded %+v", tc.name, report.NotLoaded)
		}
		if got := runFiltered(t, out); got != tc.want {
			t.Errorf("%s: output %q, want %q\nfiltered:\n%s", tc.name, got, tc.want, out)
		}
	}
}

// zsh's (( $+commands[x] )) asks whether x is a command on the PATH: it
// runs as bash's command -v x, so the guard decides as in zsh instead of
// being skipped with what it guards. Only the arithmetic command that is
// exactly that test, x a word or a parameter, is rewritten. The outputs
// are zsh -f's.
func TestFilterRC_CommandsTestIsCommandV(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"word", "(( $+commands[ls] )) && echo yes\n(( $+commands[no_such_cmd_xyz] )) || echo no\n", "yes\nno\n"},
		{"braces", "(( ${+commands[ls]} )) && echo yes\n", "yes\n"},
		{"negated inside", "(( ! $+commands[no_such_cmd_xyz] )) && echo none\n", "none\n"},
		{"negated outside", "! (( $+commands[ls] )) || echo has\n", "has\n"},
		{"parameter", "has() { (( $+commands[$1] )); }\nhas ls && echo yes\nhas no_such_cmd_xyz || echo no\n", "yes\nno\n"},
		// zsh keeps the quotes in the key: no command is named "ls".
		{"quoted parameter", "has() { (( $+commands[\"$1\"] )) }\nif has ls; then echo GUARDED; fi\necho end\n", "end\n"},
		{"variable", "x=ls\n(( ${+commands[$x]} )) && echo yes\n", "yes\n"},
		{"plugin guard", "if (( ! $+commands[ls] )); then\n  return\nfi\nalias k=kubectl\necho rest\n", "rest\n"},
		{"more than the test", "(( $+commands[ls] + 0 )) && echo GUARDED\necho end\n", "end\n"},
		// The zsh parser reads a name with - or + in it as arithmetic:
		// the name is the text between the brackets.
		{"hyphen", "(( $+commands[no-such-cmd] )) || echo no\n", "no\n"},
		{"hyphen guard", "if (( ! $+commands[no-such-cmd] )); then\n  echo none\nfi\necho rest\n", "none\nrest\n"},
		{"plus", "(( $+commands[no+such] )) || echo no\n", "no\n"},
		{"plus plus", "(( $+commands[nosuch++] )) || echo no\n", "no\n"},
		{"spaces are no name", "(( $+commands[ ls ] )) && echo GUARDED\necho end\n", "end\n"},
		// && || ! of terms in one (( )): && binds tighter there, the
		// shell's && and || are of one precedence.
		{"and of negations", "(( ! $+commands[no_such_a] && ! $+commands[no_such_b] )) && echo neither\n", "neither\n"},
		{"or then and", "(( $+commands[ls] || $+commands[no_such_a] && $+commands[no_such_b] )) && echo yes\n", "yes\n"},
		{"negated group", "(( ! ( $+commands[ls] && $+commands[no_such_a] ) )) && echo yes\n", "yes\n"},
		{"negated outside a chain", "! (( $+commands[ls] && $+commands[no_such_a] )) && echo yes\n", "yes\n"},
		{"negated twice", "! (( ! $+commands[ls] )) && echo yes\n(( ! ! $+commands[ls] )) && echo yes\n", "yes\nyes\n"},
		{"chain with more", "(( $+commands[ls] && 1 )) && echo GUARDED\necho end\n", "end\n"},
	} {
		out, report := filterBash(t, tc.src)
		if len(report.NotLoaded) > 0 {
			t.Errorf("%s: not loaded %+v", tc.name, report.NotLoaded)
		}
		if got := runFiltered(t, out); got != tc.want {
			t.Errorf("%s: output %q, want %q\nfiltered:\n%s", tc.name, got, tc.want, out)
		}
		if strings.Contains(tc.src, "alias") && report.Summary.Aliases != 1 {
			t.Errorf("%s: the alias after the guard is not counted: %+v", tc.name, report.SkippedItems)
		}
	}
}

// The interpreter cannot evaluate $+commands in the zsh dialect either: the
// tests become command -v there too.
func TestFilterRC_CommandsTestInTheZshDialect(t *testing.T) {
	rcFile := filepath.Join(t.TempDir(), ".zshrc")
	src := "(( $+commands[ls] )) && echo yes\n(( ! $+commands[no-such-cmd] && $+commands[ls] )) && echo both\n"
	if err := os.WriteFile(rcFile, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := FilterWithDialect(rcFile, "zsh", "zsh")
	if err != nil {
		t.Fatal(err)
	}
	want := "command -v ls >/dev/null 2>&1 && echo yes\n! command -v no-such-cmd >/dev/null 2>&1 && command -v ls >/dev/null 2>&1 && echo both\n"
	if out != want {
		t.Errorf("filtered %q, want %q", out, want)
	}
}
