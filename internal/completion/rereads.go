package completion

import (
	"errors"
	"path"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// codeKind is how a command reads the words after it.
type codeKind uint8

const (
	// runsCommand: it runs the command its arguments name, after its
	// options and operands (sudo, env, timeout, flock, BSD script).
	runsCommand codeKind = iota
	// takesCode: only its code options' arguments are code; its other
	// words are operands, wherever they are (su root -c cmd).
	takesCode
	// allCode: every argument is code (eval, alias, a trap's action,
	// watch, which joins its arguments for sh -c).
	allCode
	// shell: the first word after its option word holding a code letter
	// is a script (sh -c, bash -lc, fish --command), and so is one
	// attached to that option, for a shell that reads it so (fish).
	shell
)

// codeReader is how a command reads its arguments, for the words it
// parses again as code.
type codeReader struct {
	kind  codeKind
	short string   // letters of its options that take an argument
	long  []string // its long options that take one (--user root)
	// code and codeLong are its options whose argument is a command it
	// runs with sh -c (flock -c, su --command), or a shell's option
	// that makes its first operand a script.
	code     string
	codeLong []string
	// operands are the words before the command (timeout's duration,
	// flock's lock file).
	operands int
	// rest: the words after a code option's argument are code too (env
	// -S splits its argument into a command those words are arguments
	// of).
	rest bool
	// attachedScript: a shell takes a script attached to its code option
	// (fish -c"cmd", fish --command=cmd). Other shells read -c"cmd" as a
	// cluster of options.
	attachedScript bool
}

// shellReader reads a shell's arguments: sh -c script, fish --command.
var shellReader = codeReader{kind: shell, code: "c", codeLong: []string{"--command"}}

// fishReader reads fish's arguments, whose -c takes its script attached
// too, as getopt does.
var fishReader = codeReader{kind: shell, code: "c", codeLong: []string{"--command"}, attachedScript: true}

// codeReaders are the commands that parse a word of theirs again as code,
// or run a command that may.
var codeReaders = map[string]codeReader{
	"eval": {kind: allCode}, "alias": {kind: allCode}, "trap": {kind: allCode}, "watch": {kind: allCode},
	"sh": shellReader, "bash": shellReader, "zsh": shellReader, "dash": shellReader,
	"ksh": shellReader, "fish": fishReader, "hash": shellReader,
	"su": {kind: takesCode, short: "gGsw", code: "c",
		long:     []string{"--group", "--supp-group", "--shell", "--whitelist-environment"},
		codeLong: []string{"--command", "--session-command"}},
	"command": {}, "builtin": {}, "nohup": {},
	"exec": {short: "a"},
	"sudo": {short: "CDghpRrTtUu", long: []string{
		"--chdir", "--chroot", "--close-from", "--command-timeout", "--group", "--host",
		"--other-user", "--prompt", "--role", "--type", "--user",
	}},
	"doas": {short: "Cu"},
	"env": {short: "CPu", long: []string{"--chdir", "--unset"},
		code: "S", codeLong: []string{"--split-string"}, rest: true},
	"timeout": {short: "ks", long: []string{"--kill-after", "--signal"}, operands: 1},
	"nice":    {short: "n", long: []string{"--adjustment"}},
	"flock": {short: "Ew", long: []string{"--conflict-exit-code", "--timeout", "--wait"}, operands: 1,
		code: "c", codeLong: []string{"--command"}},
	// util-linux script -c cmd [file]; BSD script [file [command ...]]
	"script": {short: "BEIOTmo", long: []string{
		"--echo", "--log-in", "--log-out", "--log-io", "--log-timing", "--logging-format", "--output-limit",
	}, operands: 1, code: "c", codeLong: []string{"--command"}},
}

// rereads reports whether the command of words args parses its argument
// args[i] again as code, as codeReaders read the command and the commands
// it runs. A runner it does not know (xargs, chroot, stdbuf) fails
// closed: any word before args[i] that names a command reading code other
// than as its arguments (sh -c, su -c, but not echo eval) is read as that
// command.
func rereads(args []*syntax.Word, i int) bool {
	lits := make([]string, i+1)
	for j := range lits {
		lits[j] = literal(args[j])
	}
	for cmd := range i {
		r, ok := codeReaders[path.Base(lits[cmd])]
		if ok && (cmd == 0 || r.kind != allCode) && readsAsCode(lits, args[i], cmd) {
			return true
		}
	}
	return false
}

// readsAsCode reports whether the command lits[cmd] names, or a command it
// runs, reads the last of lits, word, as code.
func readsAsCode(lits []string, word *syntax.Word, cmd int) bool {
	i := len(lits) - 1
	for cmd < i {
		r, ok := codeReaders[path.Base(lits[cmd])]
		switch {
		case !ok:
			return false
		case r.kind == allCode:
			return true
		case r.kind == shell:
			return r.script(lits, cmd) == i || r.attachedScript && r.attached(word)
		}
		next, code := r.skip(lits, word, cmd+1)
		if code || r.kind != runsCommand {
			return code
		}
		cmd = next
	}
	return false
}

// skip reads a command's words from lits[j] to the last of lits, word:
// past its options (and an option's argument: sudo -u root, sudo -uroot,
// sudo --user root) and operands. It returns the index of the command
// they run (the last when none comes before it), and whether word is
// code: a code option's argument, attached to it or not, or a word after
// one that leaves the rest code.
func (r codeReader) skip(lits []string, word *syntax.Word, j int) (cmd int, code bool) {
	i := len(lits) - 1
	operands, options := r.operands, true
	for ; j < i; j++ {
		switch lit := lits[j]; {
		case options && lit == "--":
			options = false
		case options && optionLike(lit):
			code, next := r.optionArg(lit)
			if code && r.rest {
				return i, true
			}
			if next {
				j++
				if j == i {
					return i, code
				}
			}
		case r.kind == runsCommand && operands == 0:
			return j, false
		default:
			operands--
		}
	}
	return i, options && r.attached(word)
}

// optionArg reads an option word: a long option (--user root) or a
// cluster (-uroot, -iu root). It returns whether the argument of the
// option in it that takes one is code, and whether that argument is the
// next word, not attached (-uroot, --user=root).
func (r codeReader) optionArg(lit string) (code, next bool) {
	if strings.HasPrefix(lit, "--") {
		name, _, attached := strings.Cut(lit, "=")
		code = slices.Contains(r.codeLong, name)
		return code, !attached && (code || slices.Contains(r.long, name))
	}
	if len(lit) < 2 || lit[0] != '-' {
		return false, false // +O, or an assignment
	}
	for k := 1; k < len(lit); k++ {
		code = strings.IndexByte(r.code, lit[k]) >= 0
		if code || strings.IndexByte(r.short, lit[k]) >= 0 {
			return code, k == len(lit)-1
		}
	}
	return false, false
}

// attached reports whether word w is a code option with the command it
// runs attached: -c"cmd", -qccmd, --command=cmd. Its head is read as
// typed, so an expansion after it does not hide it.
func (r codeReader) attached(w *syntax.Word) bool {
	if len(w.Parts) == 0 {
		return false
	}
	head, ok := w.Parts[0].(*syntax.Lit)
	if !ok || !strings.HasPrefix(head.Value, "-") {
		return false
	}
	code, next := r.optionArg(head.Value)
	return code && (!next || len(w.Parts) > 1)
}

// script is the index in lits of the script a shell, lits[cmd], runs: the
// first word after its option word holding c (or --command), or -1.
func (r codeReader) script(lits []string, cmd int) int {
	c := false
	for j := cmd + 1; j < len(lits); j++ {
		lit := lits[j]
		switch {
		case !optionLike(lit) && c:
			return j
		case strings.HasPrefix(lit, "--"):
			c = c || slices.Contains(r.codeLong, lit)
		case strings.HasPrefix(lit, "-") && strings.ContainsAny(lit[1:], r.code):
			c = true
		}
	}
	return -1
}

// literal is the text word w means with nothing expanded (\eval and
// 'eval' are eval), or "" for a word that expands. A word of plain text
// means itself, without the cost of expanding it: a menu reads every word
// of a long command for each item.
func literal(w *syntax.Word) string {
	if len(w.Parts) == 1 {
		if lit, ok := w.Parts[0].(*syntax.Lit); ok && !strings.ContainsAny(lit.Value, "\\~{*?[") {
			return lit.Value
		}
	}
	fields, err := expand.Fields(&expand.Config{Env: expand.ListEnviron(), NoUnset: true, ProcSubst: noProcSubst}, w)
	if err != nil || len(fields) != 1 {
		return ""
	}
	return fields[0]
}

// noProcSubst fails a process substitution, <( ) or >( ): a word with one
// expands. (The expander calls its handler for one without asking whether
// there is any, where it fails a command substitution that has none.)
func noProcSubst(*syntax.ProcSubst) (string, error) {
	return "", errors.New("a process substitution expands")
}

// optionLike reports whether a word is an option (-c, +O) or an env
// assignment (X=1), not a command or a script.
func optionLike(lit string) bool {
	return strings.HasPrefix(lit, "-") && lit != "-" || strings.HasPrefix(lit, "+") || strings.Contains(lit, "=")
}
