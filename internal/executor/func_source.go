package executor

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"

	"github.com/tfcace/hash/internal/shparse"
)

// The shell keeps a function as hash prepared it (prepareProgram), with
// the calls hash runs its jobs and its substitutions through and the array
// an interactive shell reads $- from, and the interpreter's declare -f
// prints that. hash's prints the function as it was written
// (functionSource), which defines the same function in bash, and in hash
// again. The rewrites that read as the shell's own syntax (>| as >, a
// pipeline's last command in a subshell, a pattern's quoted text escaped)
// stay.

// functionQuery makes each `declare -f NAME...` (and typeset) in prog a
// plain call of the same words, which reaches hash's handler
// (functionSourceCall) where a declaration reaches only the interpreter.
// Printed, the call is the declaration again.
func functionQuery(prog *syntax.File) {
	syntax.Walk(prog, func(node syntax.Node) bool {
		st, ok := node.(*syntax.Stmt)
		if !ok {
			return true
		}
		decl, ok := st.Cmd.(*syntax.DeclClause)
		if !ok || (decl.Variant.Value != "declare" && decl.Variant.Value != "typeset") {
			return true
		}
		words := []*syntax.Word{litWord(decl.Variant.Value)}
		for _, as := range decl.Args {
			switch {
			case as.Name == nil && as.Value != nil && as.Value.Lit() != "":
				words = append(words, as.Value)
			case as.Naked && as.Name != nil && as.Index == nil && as.Value == nil && as.Array == nil:
				words = append(words, litWord(as.Name.Value))
			default:
				return true
			}
		}
		if _, ok := functionQueryNames(wordLits(words)); ok {
			st.Cmd = &syntax.CallExpr{Args: words}
		}
		return true
	})
}

// wordLits is the text of each of words, literals all.
func wordLits(words []*syntax.Word) []string {
	lits := make([]string, len(words))
	for i, w := range words {
		lits[i] = w.Lit()
	}
	return lits
}

// functionQueryNames is the functions the call args, declare or typeset,
// asks the source of: `declare -f NAME...`, -f its only option. ok is
// false for any other call.
func functionQueryNames(args []string) (names []string, ok bool) {
	if len(args) < 3 || args[1] != "-f" {
		return nil, false
	}
	for _, name := range args[2:] {
		if !syntax.ValidName(name) {
			return nil, false
		}
	}
	return args[2:], true
}

// functionSourceCall prints the functions names of the calling shell, as
// bash's declare -f, and returns the call that carries its status: 1 when
// one of them is no function, which it passes over silently.
func (e *Executor) functionSourceCall(ctx context.Context, names []string) []string {
	funcs := e.callerRunner(ctx).Funcs
	status := 0
	for _, name := range names {
		body := funcs[name]
		if body == nil {
			status = 1
			continue
		}
		fmt.Fprintf(interp.HandlerCtx(ctx).Stdout, "%s()\n%s\n", name, e.functionSource(body))
	}
	return statusCall(status)
}

// functionSource is the function body, as the shell keeps it, printed as
// it was written: without what prepareProgram added that is no syntax of
// the shell's. The body is not touched (the function may be running): it
// is printed, the additions are cut out of that text (cutPrepared), and
// the text is parsed and printed again with each $- itself. A body that
// does not parse again prints as it is.
func (e *Executor) functionSource(body *syntax.Stmt) string {
	var kept strings.Builder
	if err := syntax.NewPrinter().Print(&kept, body); err != nil {
		return ""
	}
	asKept := strings.TrimSuffix(kept.String(), "\n")
	prog, err := shparse.Parse(e.newParser(), kept.String(), "")
	if err != nil {
		return asKept
	}
	prog, err = shparse.Parse(e.newParser(), e.cutPrepared(kept.String(), prog), "")
	if err != nil {
		return asKept
	}
	syntax.Walk(prog, func(node syntax.Node) bool {
		if p, ok := node.(*syntax.ParamExp); ok && p.Param != nil && p.Param.Value == flagsVar {
			if key := flagsKey(p); key != nil { // markInteractive's ${__hash_flags[$-]}
				p.Param, p.Index = key.Param, nil
				p.Short = !p.Excl && !p.Length && !p.Width && p.Slice == nil && p.Repl == nil && p.Names == 0 && p.Exp == nil
			}
		}
		return true
	})
	var written strings.Builder
	if err := syntax.NewPrinter().Print(&written, prog); err != nil {
		return asKept
	}
	return strings.TrimSuffix(written.String(), "\n")
}

// cut is a piece of a program's text, from start to end, and what goes in
// its place.
type cut struct {
	start, end uint
	with       string
}

// cutPrepared is text, the program prog printed, with each job its own
// statement again in place of the two calls markJobs made of it, and
// without the calls that open and end a substitution that starts jobs
// (endSubstitutionsWithTheirJobs). The cuts are made in the text, a line
// at a time, so the lines of what stays follow each other as they were
// written.
func (e *Executor) cutPrepared(text string, prog *syntax.File) string {
	var cuts []cut
	syntax.Walk(prog, func(node syntax.Node) bool {
		if cs, ok := node.(*syntax.CmdSubst); ok {
			if last := len(cs.Stmts) - 1; last >= 2 && callsOnly(cs.Stmts[0], substBeginCommand) && callsOnly(cs.Stmts[last], substEndCommand) {
				cuts = append(cuts,
					cut{start: cs.Stmts[0].Pos().Offset(), end: cs.Stmts[1].Pos().Offset()},
					cut{start: cs.Stmts[last-1].End().Offset(), end: cs.Stmts[last].End().Offset()})
			}
		}
		for _, list := range stmtLists(node) {
			for i, st := range *list {
				own, ok := e.jobText(st)
				if !ok {
					continue
				}
				end := st.End()
				if i+1 < len(*list) && callsOnly((*list)[i+1], jobStartedCommand) {
					end = (*list)[i+1].End()
				}
				cuts = append(cuts, cut{start: st.Pos().Offset(), end: end.Offset() + blankLineAt(text, end.Offset()), with: own})
			}
		}
		return true
	})
	slices.SortFunc(cuts, func(a, b cut) int { return cmp.Compare(b.start, a.start) })
	for _, c := range cuts {
		text = text[:c.start] + c.with + text[c.end:]
	}
	return text
}

// blankLineAt is the length of the blank line that follows the line
// ending at at in text, 0 when the next line is not blank: the printer
// leaves one after the calls markJobs made, which have no place in the
// text they were parsed from.
func blankLineAt(text string, at uint) uint {
	rest := text[at:]
	if !strings.HasPrefix(rest, "\n") {
		return 0
	}
	line, _, more := strings.Cut(rest[1:], "\n")
	if !more || strings.TrimSpace(line) != "" {
		return 0
	}
	return uint(1 + len(line))
}

// jobText is the statement of the job st runs, as markJobs made it
// (`{ __hash_job_run ID 'text'; } &`), printed from the text it carries
// with its own & (or &!); ok is false when st is no such job, or its text
// is not one statement.
func (e *Executor) jobText(st *syntax.Stmt) (text string, ok bool) {
	block, isBlock := st.Cmd.(*syntax.Block)
	if !isBlock || (!st.Background && !st.Disown) || len(block.Stmts) != 1 || len(st.Redirs) > 0 {
		return "", false
	}
	call, isCall := block.Stmts[0].Cmd.(*syntax.CallExpr)
	if !isCall || len(call.Args) != 3 || call.Args[0].Lit() != jobRunCommand {
		return "", false
	}
	carried, ok := quotedWordText(call.Args[2])
	if !ok {
		return "", false
	}
	prog, err := shparse.Parse(e.newParser(), carried, "")
	if err != nil || len(prog.Stmts) != 1 {
		return "", false
	}
	prog.Stmts[0].Background, prog.Stmts[0].Disown = st.Background, st.Disown
	var b strings.Builder
	if err := syntax.NewPrinter().Print(&b, prog); err != nil {
		return "", false
	}
	return strings.TrimSuffix(b.String(), "\n"), true
}

// quotedWordText is the text w expands to, w being a word quotedWord
// made; ok is false for any other word.
func quotedWordText(w *syntax.Word) (text string, ok bool) {
	var b strings.Builder
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.SglQuoted:
			if p.Dollar {
				return "", false
			}
			b.WriteString(p.Value)
		case *syntax.Lit:
			if p.Value != `\'` {
				return "", false
			}
			b.WriteByte('\'')
		default:
			return "", false
		}
	}
	return b.String(), true
}

// callsOnly reports whether st is a call of the command name and nothing
// else, whatever its arguments.
func callsOnly(st *syntax.Stmt, name string) bool {
	call, ok := st.Cmd.(*syntax.CallExpr)
	return ok && len(call.Args) > 0 && call.Args[0].Lit() == name && len(call.Assigns) == 0 &&
		len(st.Redirs) == 0 && !st.Negated && !st.Background && !st.Disown
}
