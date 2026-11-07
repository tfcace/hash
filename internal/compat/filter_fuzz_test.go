package compat

import "testing"

// The filter's output always parses in the target dialect, or is empty:
// whatever is in the rc file, sourcing it never fails as a whole. That
// skipped statements never run is FuzzFilterRC_ExecutingOracle's: it runs
// the output (this fuzzer's static guard check took a case arm matching the
// skipped header's own word for one that never runs).
func FuzzFilterOutputParses(f *testing.F) {
	for _, seed := range []string{
		"(( z = x << y ))\nsetopt autocd\n",
		"echo foo;# it's fine\nsetopt autocd\n",
		"export X=\"$(echo \"it's\")\"\n[[ -n \"${(%):-%n}\" ]] && echo hi\n",
		"cat <<EOF\nsetopt x\nEOF\nbindkey -v\n",
		"x=$(cat <<EOF\nit's\nEOF\n)\n",
		"zstyle ':x' format \"a\nb\"\nexport K=1\n",
		"if [[ -n \"${(%):-%n}\" ]]; then\n  echo\nfi\n",
		"echo `echo \"it's\"`\n",
		"x=$(( 16#ff ))\ny=$(( (1+2) << 3 ))\n",
		"echo a \\\n  b\nsetopt x\n",
		"typeset -a dirs=(\n  ~/src\n)\n",
		"for f in ~/.zsh/*.zsh(N); do\n  source $f\ndone\n",
		"[[ $TERM == (#i)xterm* ]] && echo x\nexport K=1\n",
		"export FPATH=\"${(j.:.)fpath}\"\n",
		"precmd() { print -Pn \"..\" }\nexport K=1\n",
		"until [[ -n \"${(%):-%n}\" ]]; do\n  echo\ndone\n",
		"[[ -n \"${(%):-%n}\" ]] && {\n  echo GUARDED\n}\n",
		"[[ -n \"${(%):-%n}\" ]] &&\n  echo GUARDED\n",
		"case ${(%):-%n} in\n  root)\n    echo GUARDED\n    ;;\nesac\n",
		"[[ $TERM == (#i)dumb ]] || {\n  echo GUARDED\n}\n",
		"function zle-line-init zle-keymap-select {\n  echo GUARDED\n}\n",
		"setopt x && {\n  echo GUARDED\n}\nbindkey -v |\n  echo GUARDED\n",
		"repeat 3 do\n  echo GUARDED\ndone\n",
		"echo \"${x:-\"it's\"}\"\nsetopt x\n",
		"(){\n\"\" }\n",
		">0()\n>0()\n\"\"\n",
		">0( )\n\"\"\n",
		"echo a\n\xff\n",
		"f() {\n  echo a\n  return 0\n  echo b\n}  2>${(%):-/dev/null}\nalias g=git\n",
		"f() {\n  [[ -n $1 ]] || return\n  echo b\n} >>*(N)\necho end\n",
		"! (( ${+commands[$x]} )) && return\n(( ! $+commands[\"$1\"] )) || exit\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, content string) {
		for _, dialect := range []string{"bash", "zsh"} {
			out, report := filterRC(content, "zsh", dialect, func() *Report { return NewReport("", "") })
			if out == "" {
				continue
			}
			if _, err := parseRC(out, dialect); err != nil {
				t.Fatalf("%s: filtered output does not parse (%v)\ninput:\n%s\noutput:\n%s\nskipped: %+v",
					dialect, err, content, out, report.SkippedItems)
			}
		}
	})
}
