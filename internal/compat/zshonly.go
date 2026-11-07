// internal/compat/zshonly.go
package compat

import (
	"regexp"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// zshBuiltins are zsh builtins bash has not; skipped from zsh files only,
// as hash's zsh dialect may run them.
var zshBuiltins = map[string]string{
	"print":   "zsh print builtin",
	"emulate": "zsh emulation mode",
}

// tiedArrays are zsh's arrays tied to a colon-separated variable (path to
// PATH); bash has them as plain arrays, so assigning one changes nothing.
var tiedArrays = map[string]bool{"path": true, "fpath": true, "cdpath": true, "manpath": true}

// zshAssignReason returns why assignment a means something else in bash,
// or "": it assigns a tied array, or an array element by zsh's index, which
// counts from 1 (bash's from 0).
func zshAssignReason(a *syntax.Assign) string {
	switch {
	case a.Name != nil && tiedArrays[a.Name.Value]:
		return "zsh tied array " + a.Name.Value + " (bash does not tie it to " + strings.ToUpper(a.Name.Value) + ")"
	case zshIndex(a.Index):
		return "zsh array index (zsh counts from 1)"
	}
	return ""
}

// zshIndex reports whether index is one zsh reads differently from bash: a
// number from 0 up (zsh counts from 1) or a range (2,3). Negative indexes
// count from the end in both.
func zshIndex(index syntax.ArithmExpr) bool {
	switch x := index.(type) {
	case *syntax.Word:
		n, err := strconv.Atoi(x.Lit())
		return err == nil && n >= 0
	case *syntax.BinaryArithm:
		return x.Op == syntax.Comma
	}
	return false
}

// bashRedirOps are what zsh's clobbering redirections are in hash, which
// has no noclobber (its interpreter takes no >| either): >| is >, and >>|
// &>| &>>| are >> &> &>>. zsh's ! in place of | (>!) is a file named ! for
// bash.
var bashRedirOps = map[syntax.RedirOperator]string{
	syntax.RdrClob:    ">",
	syntax.AppClob:    ">>",
	syntax.RdrAllClob: "&>",
	syntax.AppAllClob: "&>>",
}

// respellRedirect returns the edit that puts redirection r in bash's
// spelling, if it needs one.
func (t *treeFilter) respellRedirect(r *syntax.Redirect) (edit, bool) {
	bash, ok := bashRedirOps[r.Op]
	if !ok {
		return edit{}, false
	}
	start := off(r.OpPos)
	end := start + len(r.Op.String())
	if t.src[start:end] == bash {
		return edit{}, false
	}
	return edit{kind: editInsert, start: start, end: end, text: bash}, true
}

var (
	// pathParamRE is $path as a whole element of a path list.
	pathParamRE = regexp.MustCompile(`^"?\$(path|\{path(\[[@*]\])?\})"?$`)
	// envParamRE is an upper-case variable, which holds no array.
	envParamRE = regexp.MustCompile(`^\$([A-Z_][A-Z0-9_]*|\{[A-Z_][A-Z0-9_]*\})$`)
)

// pathExport returns the export of PATH that zsh makes of s, path=(...) or
// path+=(...) of a plain list: words, quoted strings, $path, and upper-case
// variables in a word with more to it. ok is false for any other statement.
func (t *treeFilter) pathExport(s *syntax.Stmt) (string, bool) {
	a := pathAssign(s)
	if a == nil {
		return "", false
	}
	var parts []string
	if a.Append {
		parts = append(parts, "$PATH")
	}
	for _, el := range a.Array.Elems {
		if el.Index != nil || el.Value == nil {
			return "", false
		}
		part, ok := t.pathElem(el.Value)
		if !ok {
			return "", false
		}
		parts = append(parts, part)
	}
	return "export PATH=" + strings.Join(parts, ":"), true
}

// pathAssign returns the assignment of s when s is path=(...) or
// path+=(...) alone, or nil.
func pathAssign(s *syntax.Stmt) *syntax.Assign {
	call, ok := s.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) > 0 || len(call.Assigns) != 1 || len(s.Redirs) > 0 ||
		s.Negated || s.Background || s.Disown || s.Coprocess {
		return nil
	}
	if a := call.Assigns[0]; a.Name.Value == "path" && a.Array != nil && a.Index == nil {
		return a
	}
	return nil
}

// pathElem returns w, an element of a plain path list, as text for PATH,
// or false. zsh drops an unquoted variable that is empty, so one that is
// the whole word is not plain.
func (t *treeFilter) pathElem(w *syntax.Word) (string, bool) {
	text := t.src[off(w.Pos()):off(w.End())]
	if pathParamRE.MatchString(text) {
		return "$PATH", true
	}
	if len(w.Parts) == 1 && envParamRE.MatchString(text) {
		return "", false
	}
	for i, part := range w.Parts {
		if !t.plainPart(part, i == 0) {
			return "", false
		}
	}
	if rest, ok := strings.CutPrefix(text, "~"); ok {
		text = "$HOME" + rest
	}
	return text, true
}

// plainPart reports whether part of a path list element, first in it or
// not, reads the same in an assignment to PATH: no globs, braces, ~user or
// =cmd, no variable that may hold an array, no substitution.
func (t *treeFilter) plainPart(part syntax.WordPart, first bool) bool {
	switch p := part.(type) {
	case *syntax.Lit:
		tilde := first && strings.HasPrefix(p.Value, "~") && p.Value != "~" && !strings.HasPrefix(p.Value, "~/")
		return !tilde && !strings.ContainsAny(p.Value, "*?[{=")
	case *syntax.SglQuoted:
		return true
	case *syntax.DblQuoted:
		for _, q := range p.Parts {
			if _, lit := q.(*syntax.Lit); !lit && !t.envParam(q) {
				return false
			}
		}
		return true
	}
	return t.envParam(part)
}

// envParam reports whether n is $NAME or ${NAME} of an upper-case name.
func (t *treeFilter) envParam(n syntax.Node) bool {
	_, ok := n.(*syntax.ParamExp)
	return ok && envParamRE.MatchString(t.src[off(n.Pos()):off(n.End())])
}

// commandWordRE is a command name as a literal index of $commands, and
// commandParamRE a parameter as one: $name, ${name} or $N.
var (
	commandWordRE  = regexp.MustCompile(`^[A-Za-z0-9_.+-]+$`)
	commandParamRE = regexp.MustCompile(`^\$([A-Za-z_][A-Za-z0-9_]*|[0-9]|\{[A-Za-z_][A-Za-z0-9_]*\})$`)
)

// commandsTest returns the bash command for s when s is zsh's test of
// whether commands are on the PATH: (( x )) with x $+commands[i] or
// ${+commands[i]}, i a word or a parameter, or such terms joined by && ||
// and ! (a ! before the (( )) stays): command -v i, joined the same way,
// with { } where the shell's && and ||, of equal precedence, would group
// them otherwise. ok is false for any other statement.
func (t *treeFilter) commandsTest(s *syntax.Stmt) (string, bool) {
	arith, isArith := s.Cmd.(*syntax.ArithmCmd)
	if !isArith || len(s.Redirs) > 0 || s.Background || s.Coprocess || s.Disown {
		return "", false
	}
	text, kind, ok := t.commandsExpr(arith.X)
	if !ok {
		return "", false
	}
	if s.Negated && kind != commandsTerm {
		text = "{ " + text + "; }"
	}
	return text, true
}

// commandsKind is what a rewritten $+commands expression is, for the
// grouping around it.
type commandsKind int

const (
	commandsTerm    commandsKind = iota // one command -v
	commandsNegated                     // ! and a command -v or a group
	commandsChain                       // commands joined by && or ||
)

// commandsExpr returns x, a $+commands expression (commandsTest), as
// commands, and what they are.
func (t *treeFilter) commandsExpr(x syntax.ArithmExpr) (string, commandsKind, bool) {
	switch x := x.(type) {
	case *syntax.ParenArithm:
		return t.commandsExpr(x.X)
	case *syntax.UnaryArithm:
		if x.Op != syntax.Not || x.Post {
			return "", 0, false
		}
		text, kind, ok := t.commandsExpr(x.X)
		if kind != commandsTerm {
			text = "{ " + text + "; }"
		}
		return "! " + text, commandsNegated, ok
	case *syntax.BinaryArithm:
		if x.Op != syntax.AndArit && x.Op != syntax.OrArit {
			return "", 0, false
		}
		left, _, okX := t.commandsExpr(x.X)
		right, kind, okY := t.commandsExpr(x.Y)
		if kind == commandsChain { // a && b || c is (a && b) || c in both
			right = "{ " + right + "; }"
		}
		return left + " " + x.Op.String() + " " + right, commandsChain, okX && okY
	}
	name, ok := t.commandsIndex(x)
	return "command -v " + name + " >/dev/null 2>&1", commandsTerm, ok
}

// commandsIndex returns the index of x, when x is $+commands[i] or
// ${+commands[i]} alone, as bash's word for it: a command name, or a
// parameter, quoted. The index is the source text between the brackets,
// which zsh takes as the key as it is (the zsh parser reads docker-compose
// or g++ as arithmetic, and zsh keeps blanks and quotes in the key).
func (t *treeFilter) commandsIndex(x syntax.ArithmExpr) (string, bool) {
	w, ok := x.(*syntax.Word)
	if !ok || len(w.Parts) != 1 {
		return "", false
	}
	p, ok := w.Parts[0].(*syntax.ParamExp)
	if !ok || !p.IsSet || p.Param == nil || p.Param.Value != "commands" || p.Index == nil || !plainParam(p, true) {
		return "", false
	}
	text := t.src[off(p.Pos()):off(p.End())]
	open, end := strings.IndexByte(text, '['), strings.LastIndexByte(text, ']')
	if open < 0 || end < open {
		return "", false
	}
	switch index := text[open+1 : end]; {
	case commandWordRE.MatchString(index):
		return index, true
	case commandParamRE.MatchString(index):
		return `"` + index + `"`, true
	}
	return "", false
}

// plainParam reports whether p is $name or ${name}, with none of zsh's or
// bash's operators on it but ${+name} when isSet is allowed (the caller has
// checked IsSet and Index).
func plainParam(p *syntax.ParamExp, isSet bool) bool {
	return p.Flags == nil && p.NestedParam == nil && len(p.Modifiers) == 0 && p.Slice == nil &&
		p.Repl == nil && p.Exp == nil && !p.Excl && !p.Length && !p.Width && p.Names == 0 &&
		p.Split == 0 && p.GlobSubst == 0 && p.RcExpand == 0 && (isSet || !p.IsSet)
}
