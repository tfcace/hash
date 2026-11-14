package executor

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// prepare readies prog, parsed by hash, to run in e's shell
// (prepareProgram).
func (e *Executor) prepare(prog *syntax.File) {
	prepareProgram(prog, e.lang, e.terminal.interactive())
}

// prepareProgram makes a parsed program, in the dialect lang, ready for
// hash's interpreter, an interactive shell's when interactive: every way
// into it (a line, a startup file, eval, source, a job's statements) goes
// through here, so no path runs a program the rewrites missed.
func prepareProgram(prog *syntax.File, lang syntax.LangVariant, interactive bool) {
	clobberAsOut(prog)
	functionQuery(prog)
	// A job's statement is printed as it was parsed, for the job's part to
	// prepare on its own (and for the jobs listing), before the rewrites
	// below reach into it.
	markBackgroundJobs(prog)
	if interactive {
		markInteractive(prog)
	}
	endSubstitutionsWithTheirJobs(prog)
	if lang == syntax.LangBash {
		pipelineTailsInSubshells(prog)
	}
	quotePatternText(prog)
	quoteEscapedChars(prog)
}

// quotePatternText makes the quoted text of each [[ == ]], [[ != ]] and
// case pattern unquoted text with its every pattern character escaped. The
// interpreter escapes only * ? [ \ in a pattern's quoted text, so the
// extglob operators @( +( !( in it opened a group, where bash matches
// them as they are (`[[ '@(x)' == '@(x)' ]]` was false). Escaped, the text
// means the same with any interpreter. Only a pattern with a ( in its
// quoted text changes. A value expanded in quotes ("$p") is still escaped
// by the interpreter alone: an extglob operator in it still opens a group.
func quotePatternText(prog *syntax.File) {
	syntax.Walk(prog, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.BinaryTest:
			if w, ok := n.Y.(*syntax.Word); ok && (n.Op == syntax.TsMatch || n.Op == syntax.TsMatchShort || n.Op == syntax.TsNoMatch) {
				escapeQuotedText(w)
			}
		case *syntax.CaseItem:
			for _, w := range n.Patterns {
				escapeQuotedText(w)
			}
		}
		return true
	})
}

// escapeQuotedText turns the quoted text in the pattern w into escaped
// unquoted text (patternEscape), when some of it holds a (. Expansions in
// double quotes stay in double quotes of their own; $'...' stays.
func escapeQuotedText(w *syntax.Word) {
	if !quotedTextHas(w, "(") {
		return
	}
	parts := make([]syntax.WordPart, 0, len(w.Parts))
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.SglQuoted:
			if p.Dollar {
				parts = append(parts, p)
				continue
			}
			parts = append(parts, &syntax.Lit{ValuePos: p.Pos(), ValueEnd: p.End(), Value: patternEscape(p.Value)})
		case *syntax.DblQuoted:
			for _, inner := range p.Parts {
				if lit, ok := inner.(*syntax.Lit); ok {
					parts = append(parts, &syntax.Lit{ValuePos: lit.ValuePos, ValueEnd: lit.ValueEnd, Value: patternEscape(dqUnescape(lit.Value))})
					continue
				}
				parts = append(parts, &syntax.DblQuoted{Left: p.Left, Right: p.Right, Parts: []syntax.WordPart{inner}})
			}
		default:
			parts = append(parts, part)
		}
	}
	w.Parts = parts
}

// quotedTextHas reports whether the quoted text in w holds any of chars.
func quotedTextHas(w *syntax.Word, chars string) bool {
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.SglQuoted:
			if !p.Dollar && strings.ContainsAny(p.Value, chars) {
				return true
			}
		case *syntax.DblQuoted:
			for _, inner := range p.Parts {
				if lit, ok := inner.(*syntax.Lit); ok && strings.ContainsAny(lit.Value, chars) {
					return true
				}
			}
		}
	}
	return false
}

// patternEscape is s as unquoted pattern text that matches s: each
// character a pattern or a word gives a meaning to escaped.
func patternEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`\*?[]@+!()|~`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// dqUnescape is the text of a literal in double quotes as the shell reads
// it: a backslash quotes $ ` " \ and a newline (which it drops with it),
// and is itself otherwise.
func dqUnescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("$`\"\\\n", s[i+1]) >= 0 {
			i++
			if s[i] == '\n' {
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// substBeginCommand and substEndCommand open and end a command
// substitution that starts jobs (endSubstitutionsWithTheirJobs): the first
// serializes the writes into it (substBegin), the second waits for its jobs
// (substEnd).
const (
	substBeginCommand = "__hash_subst_begin"
	substEndCommand   = "__hash_subst_end"
)

// endSubstitutionsWithTheirJobs opens each command substitution that starts
// a background job with a call of substBeginCommand and ends it with one of
// substEndCommand. bash reads a substitution until every writer closed its
// pipe, so a job started in it holds it open until the job ends
// (`$(slow & echo now)` is "now" and the job's output), and the writes of
// the job and the substitution reach the pipe one at a time; the
// interpreter reads its buffer once the substitution's statements
// returned, and its jobs wrote into the buffer as the substitution did and
// after it was read.
// The wait is for the substitution's own jobs that keep its output open,
// as bash's (substEnd); an exit in the substitution skips it.
func endSubstitutionsWithTheirJobs(prog *syntax.File) {
	syntax.Walk(prog, func(node syntax.Node) bool {
		if cs, ok := node.(*syntax.CmdSubst); ok && startsJobs(cs.Stmts) {
			begin := &syntax.Stmt{Cmd: &syntax.CallExpr{Args: []*syntax.Word{litWord(substBeginCommand)}}}
			end := &syntax.Stmt{Cmd: &syntax.CallExpr{Args: []*syntax.Word{litWord(substEndCommand)}}}
			cs.Stmts = append(append([]*syntax.Stmt{begin}, cs.Stmts...), end)
		}
		return true
	})
}

// startsJobs reports whether stmts hold a background statement, outside
// any command substitution of their own.
func startsJobs(stmts []*syntax.Stmt) bool {
	found := false
	for _, st := range stmts {
		syntax.Walk(st, func(node syntax.Node) bool {
			if _, ok := node.(*syntax.CmdSubst); ok {
				return false
			}
			if s, ok := node.(*syntax.Stmt); ok && (s.Background || s.Disown) {
				found = true
			}
			return !found
		})
	}
	return found
}

// clobberAsOut turns `>|` into `>`. `>|` overwrites a file that noclobber
// would protect; hash has no noclobber, so the two are the same, and the
// interpreter does not handle `>|` itself (it reports "unhandled redirect
// op" and writes nothing).
func clobberAsOut(prog *syntax.File) {
	syntax.Walk(prog, func(node syntax.Node) bool {
		if r, ok := node.(*syntax.Redirect); ok && r.Op == syntax.RdrClob {
			r.Op = syntax.RdrOut
		}
		return true
	})
}

// pipelineTailsInSubshells puts the last command of each pipeline in a
// subshell of its own, as bash runs it (without lastpipe): the interpreter
// runs it in the shell itself, as zsh does, so `echo hi | exit 3` would end
// the shell and `echo x | read v` set v. The other commands of a pipeline
// run in subshells already. The parser nests `a | b | c` as (a | b) | c:
// only the outermost pipe's tail is a pipeline's last command.
func pipelineTailsInSubshells(prog *syntax.File) {
	inner := map[*syntax.BinaryCmd]bool{}
	syntax.Walk(prog, func(node syntax.Node) bool {
		pipe, ok := node.(*syntax.BinaryCmd)
		if !ok || (pipe.Op != syntax.Pipe && pipe.Op != syntax.PipeAll) || inner[pipe] {
			return true
		}
		for x := pipe.X; x != nil; {
			head, ok := x.Cmd.(*syntax.BinaryCmd)
			if !ok || (head.Op != syntax.Pipe && head.Op != syntax.PipeAll) {
				break
			}
			inner[head] = true
			x = head.X
		}
		if _, ok := pipe.Y.Cmd.(*syntax.Subshell); !ok {
			pipe.Y = &syntax.Stmt{Cmd: &syntax.Subshell{Stmts: []*syntax.Stmt{pipe.Y}}}
		}
		return true
	})
}

// markInteractive makes prog see the interactive shell, which the
// interpreter has no flag for: each expansion of $-, whatever its operator
// (${-#*e}, ${#-}, ${-//i}), expands the interpreter's flags with the i of
// an interactive shell in them (flagsVar), as bash's and zsh's $- holds
// one; and [[ -o interactive ]] is [[ -n interactive ]], true, as in zsh.
// A program marked already, printed (declare -f, a sourced startup file)
// and parsed again, stays as it is.
func markInteractive(prog *syntax.File) {
	var dashes []*syntax.ParamExp
	marked := map[*syntax.ParamExp]bool{} // the $- keys of flagsVar
	syntax.Walk(prog, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.ParamExp:
			switch {
			case n.Param == nil:
			case n.Param.Value == flagsVar:
				if key := flagsKey(n); key != nil {
					marked[key] = true
				}
			case n.Param.Value == "-" && n.Index == nil && !marked[n]:
				dashes = append(dashes, n)
			}
		case *syntax.UnaryTest:
			if w, ok := n.X.(*syntax.Word); ok && n.Op == syntax.TsOptSet && w.Lit() == "interactive" {
				n.Op = syntax.TsNempStr
			}
		}
		return true
	})
	for _, p := range dashes {
		key := &syntax.ParamExp{Dollar: p.Dollar, Short: true, Param: &syntax.Lit{ValuePos: p.Param.ValuePos, ValueEnd: p.Param.ValueEnd, Value: "-"}}
		p.Short = false
		p.Param = &syntax.Lit{ValuePos: p.Param.ValuePos, ValueEnd: p.Param.ValueEnd, Value: flagsVar}
		p.Index = &syntax.Word{Parts: []syntax.WordPart{key}}
	}
}

// flagsKey is the $- of ${__hash_flags[$-]}, p, or nil.
func flagsKey(p *syntax.ParamExp) *syntax.ParamExp {
	w, ok := p.Index.(*syntax.Word)
	if !ok || len(w.Parts) != 1 {
		return nil
	}
	key, ok := w.Parts[0].(*syntax.ParamExp)
	if !ok || key.Param == nil || key.Param.Value != "-" {
		return nil
	}
	return key
}
