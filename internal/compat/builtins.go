// internal/compat/builtins.go
package compat

import (
	"path"
	"strings"
)

// noopBuiltins maps the zsh builtins that have no bash counterpart to why a
// line running one is skipped.
var noopBuiltins = map[string]string{
	"bindkey":    "zsh key binding (use Hash keybindings config)",
	"setopt":     "zsh shell option (use Hash config)",
	"unsetopt":   "zsh shell option (use Hash config)",
	"autoload":   "zsh function autoloading",
	"compdef":    "zsh completion definition (use Hash completion)",
	"zstyle":     "zsh style configuration",
	"zmodload":   "zsh module loading",
	"zle":        "zsh line editor",
	"compinit":   "zsh completion init",
	"promptinit": "zsh prompt init",
	"repeat":     "zsh repeat loop (its body is skipped with it)",
	// add-zsh-hook is an autoloaded zsh function, not a builtin, but nothing
	// defines it outside zsh.
	"add-zsh-hook": "zsh hook function",
}

// aliasSkipReason returns why an alias line is skipped: zsh's global
// (-g) and suffix (-s) aliases, which bash's alias does not have.
func aliasSkipReason(line string) string {
	words := codeWords(line)
	if len(words) == 0 || words[0] != "alias" {
		return ""
	}
	for _, w := range words[1:] {
		if !strings.HasPrefix(w, "-") || w == "--" {
			break
		}
		switch {
		case strings.Contains(w, "g"):
			return "zsh global alias (alias -g)"
		case strings.Contains(w, "s"):
			return "zsh suffix alias (alias -s)"
		}
	}
	return ""
}

// declareFlags are the flags the interpreter's typeset (declare) takes, all
// with -: it rejects the rest of bash's (-i, -l, -u, +x, ...) with an
// invalid option error, so the declaration does not happen.
const declareFlags = "aAfgnprx"

// bashDeclareFlags are the flags bash's typeset (declare) takes.
const bashDeclareFlags = "aAfFgiIlnprtux"

// typesetSkipReason returns why a typeset or declare line is skipped: a flag
// the interpreter does not take, zsh's (-U, -T, -Z, ...) or bash's, or --,
// which it takes for a name (declare: invalid name "--") and so declares
// nothing. With flags it takes, typeset runs as bash's declare, in either
// dialect: the executor is bash's interpreter.
func typesetSkipReason(line string) string {
	words := codeWords(line) // a quoted value ("a -i") is no flag
	if len(words) == 0 || (words[0] != "typeset" && words[0] != "declare") {
		return ""
	}
	for _, w := range words[1:] {
		if w == "--" {
			return words[0] + " -- that hash does not support"
		}
		if len(w) < 2 || (w[0] != '-' && w[0] != '+') {
			continue
		}
		for _, c := range w[1:] {
			switch flag := string(w[0]) + string(c); {
			case w[0] == '-' && strings.ContainsRune(declareFlags, c):
			case strings.ContainsRune(bashDeclareFlags, c):
				return words[0] + " flag " + flag + " that hash does not support"
			default:
				return "zsh-only " + words[0] + " flag " + flag
			}
		}
	}
	return ""
}

// shellNames are the shells an exec of one leaves hash for.
var shellNames = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ksh": true, "mksh": true,
	"tcsh": true, "csh": true, "nu": true, "xonsh": true, "elvish": true, "pwsh": true,
}

// execShellReason returns why a command, text as written, is skipped as an
// exec of a shell, or "". In a file written for another shell (a migrated
// .bashrc's exec zsh, or exec $SHELL) it would leave hash for good at
// startup.
func execShellReason(text string) string {
	toks := codeTokens(text)
	for i, tok := range toks {
		if tok != "exec" || (i > 0 && !isOperator(toks[i-1])) {
			continue
		}
		j := i + 1
		for j < len(toks) && strings.HasPrefix(toks[j], "-") { // -c, -l, -a name, --
			if toks[j] == "-a" {
				j++
			}
			j++
		}
		if j < len(toks) && isShellWord(toks[j]) {
			return "exec of another shell (it would leave hash)"
		}
	}
	return ""
}

// isShellWord reports whether word, a command name as written, names a
// shell: one of shellNames, by any path, or $SHELL.
func isShellWord(word string) bool {
	word = strings.Trim(word, `"'`)
	return word == "$SHELL" || word == "${SHELL}" || shellNames[path.Base(word)]
}
