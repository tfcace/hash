// internal/compat/tree.go
package compat

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/tfcace/hash/internal/executor"
	"github.com/tfcace/hash/internal/shparse"
	"mvdan.cc/sh/v3/syntax"
)

// filterRC filters content, an rc file written for shell ("zsh", "bash", or
// "" for the target dialect's own), for the target dialect. It works on the
// file's syntax tree (filterTree). A file the tree filter cannot take whole
// (its shell's parser rejects it, or the filter cannot make it parse) loads
// up to the statement that stops it, as bash and zsh run a file up to its
// first syntax error: the lines from there on are commented out, and the
// report says the file is not loaded from that line (NotLoaded). Each run
// fills a fresh report from newReport.
func filterRC(content, shell, dialect string, newReport func() *Report) (string, *Report) {
	lines := strings.Split(content, "\n")
	keep, kept := content, len(lines)
	var stopped *filterStop
	for {
		out, report, stop := filterTree(keep, shell, dialect, newReport)
		if stop == nil {
			if stopped == nil {
				return out, report
			}
			report.NotLoaded = append(report.NotLoaded, stopped.notLoaded(kept))
			return out + commentedOut(lines[kept:]), report
		}
		// Fewer lines every time: the loop ends, at worst with none kept.
		stopped, kept = stop, stop.linesBefore(keep, sourceLang(shell, dialect))
		keep = ""
		if kept > 0 {
			keep = strings.Join(lines[:kept], "\n") + "\n"
		}
	}
}

// filterStop is where the tree filter stopped in a file, and why. The file
// loads up to the top-level statement at source offset at, or, when the
// source parser rejected it (unfinished), up to the first statement that
// parser did not finish. With neither, nothing says where: none of it
// loads.
type filterStop struct {
	at         int // -1: unknown
	unfinished bool
	line       int // the line the parser's error is on, 0 for none
	reason     string
}

// notLoaded is the file not loaded from the stop on, its first kept lines
// loaded; the caller names the file.
func (s *filterStop) notLoaded(kept int) NotLoaded {
	if kept == 0 {
		return NotLoaded{Load: executor.LoadedNone, Line: s.line, Reason: s.reason}
	}
	return NotLoaded{Load: executor.LoadedBefore, Line: kept + 1, Reason: s.reason}
}

// linesBefore is how many lines of content come before the stop: whole
// lines of complete top-level statements, fewer than content has. It is
// none when the statement at the stop starts with a closer (done, fi, }):
// the line that opened its compound was read as something else, so the
// statements before it may be the body that compound guarded.
func (s *filterStop) linesBefore(content string, lang syntax.LangVariant) int {
	type span struct{ start, end, endOff int } // a top-level statement's first and last line, and its end
	var tops []span
	_ = shparse.Stmts(syntax.NewParser(syntax.Variant(lang)), strings.NewReader(content), func(st *syntax.Stmt) bool {
		end := stmtEnd(st)
		tops = append(tops, span{int(st.Pos().Line()), int(end.Line()), min(off(end), len(content))})
		return true
	})
	total := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		total++
	}
	k, at := 0, -1 // the lines kept, and where the statement at the stop starts
	switch {
	case s.at >= 0:
		at = min(s.at, len(content))
		k = lineOf(content, at) - 1
	case s.unfinished && len(tops) > 0:
		last := tops[len(tops)-1]
		k, at = last.end, last.endOff
		if k >= total { // the unfinished statement starts on the last one's line
			k = last.start - 1
		}
	case s.unfinished:
		at = 0
	}
	if at >= 0 && startsWithCloser(content[at:]) {
		return 0
	}
	k = min(k, total-1)
	// A statement that starts in the kept lines and ends after them is cut
	// in two: it goes too.
	for i := len(tops) - 1; i >= 0; i-- {
		if tops[i].start <= k && k < tops[i].end {
			k = tops[i].start - 1
		}
	}
	return max(k, 0)
}

// commentedOut is lines, the lines of a file from where it stopped loading,
// as comments, so the lines before them keep their numbers. The parser
// rejects invalid UTF-8 even in a comment, so it is replaced.
func commentedOut(lines []string) string {
	out := make([]string, len(lines))
	for i, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out[i] = skipComment + strings.ToValidUTF8(trimmed, "\uFFFD")
		}
	}
	return strings.Join(out, "\n")
}

// sourceLang is the language an rc file written for shell is parsed in.
func sourceLang(shell, dialect string) syntax.LangVariant {
	switch shell {
	case "zsh":
		return syntax.LangZsh
	case "bash":
		return syntax.LangBash
	}
	return langVariantForDialect(dialect)
}

// filterTree parses content in its own language and replaces each statement
// the target must not run with a no-op, in the source text, so that every
// other line stays as written and keeps its line number. The result must
// parse in the target dialect: a statement the target parser rejects is
// replaced too, and the file filtered again, up to maxRepairs times. When
// it cannot do that, it returns where it stopped and why.
func filterTree(content, shell, dialect string, newReport func() *Report) (string, *Report, *filterStop) {
	src, target := sourceLang(shell, dialect), langVariantForDialect(dialect)
	file, err := shparse.Parse(syntax.NewParser(syntax.Variant(src)), content, "")
	if err != nil {
		return "", nil, &filterStop{at: -1, unfinished: true, line: parseErrorLine(err), reason: parseErrorText(err)}
	}
	t, stop := newTreeFilter(content, file, dialect, src == syntax.LangZsh, target != syntax.LangZsh)
	if stop != nil {
		return "", nil, stop
	}
	t.foreign = shell != ""
	for range maxRepairs {
		if stop := t.walk(); stop != nil {
			return "", nil, stop
		}
		out, prog := content, file
		if len(t.set.edits) > 0 || src != target {
			var spans []outSpan
			out, spans = t.render()
			if prog, err = parseRC(out, dialect); err != nil {
				o := sourceOffset(spans, err)
				s := t.stmtAt(o)
				if s == nil {
					// The target rejects it where no statement can be skipped.
					stop := &filterStop{at: min(o, len(content)), reason: parseErrorText(err)}
					if o >= 0 {
						stop.line = lineOf(content, stop.at)
					}
					return "", nil, stop
				}
				t.rejected[s] = "parse error: " + parseErrorText(err)
				continue
			}
		}
		return out, t.report(newReport, prog), nil
	}
	first := len(content)
	for s := range t.rejected {
		first = min(first, off(s.Pos()))
	}
	return "", nil, &filterStop{at: first, line: lineOf(content, first), reason: fmt.Sprintf("more than %d statements do not parse", maxRepairs)}
}

// treeFilter is one rc file's syntax tree and the edits that make it safe for
// the target: walk computes them from the skip rules and the statements the
// target parser rejected so far.
type treeFilter struct {
	src     string
	file    *syntax.File
	dialect string
	// zshSource: the source is zsh, whose $+commands tests the interpreter
	// cannot run in either dialect. zshOnly: the target is not zsh either,
	// so zsh syntax that bash has not, or reads differently, is skipped too.
	zshSource, zshOnly bool
	// returnEnds: a return at the file's top level ends it, as hash runs a
	// bash target's file sourced; one in the zsh dialect may run as a line
	// (the executor's sourcedProgram), where a return ends nothing.
	returnEnds bool
	// foreign: the file is written for another shell than hash (a migrated
	// file), where an exec of a shell is skipped (execShellReason).
	foreign  bool
	rejected map[*syntax.Stmt]string // statements the target parser rejected, with why
	hdocs    []hdocSpan

	set editSet // what walk found
}

// stmtStatus is whether a statement runs as written, and so whether its
// exit status is the one the source shell would give it.
type stmtStatus int

const (
	stmtKept    stmtStatus = iota // it runs as written
	stmtSkipped                   // it is ":", which succeeds whatever it was
	// stmtUnknown: it runs, but something in it that decides its status or
	// its output (the right of && || |, the last command of a { } or a
	// branch, a $( ) in a word) is skipped.
	stmtUnknown
)

// outcome is what the filter made of a statement, as far as the statements
// around it can tell.
type outcome struct {
	status stmtStatus
	reason string // why it is not kept: the skip that made it so
}

func (o outcome) kept() bool { return o.status == stmtKept }

// with returns o, or, when o is kept and p is not, the outcome of a
// statement that runs but holds p: its status is unknown.
func (o outcome) with(p outcome) outcome {
	if !o.kept() || p.kept() {
		return o
	}
	return outcome{status: stmtUnknown, reason: p.reason}
}

// hdocSpan is a heredoc: the offset of its operator and its body's bytes.
type hdocSpan struct {
	op, start, end int
	r              *syntax.Redirect
}

// newTreeFilter returns the filter for file, parsed from src. It stops at
// the top-level statement where the parser placed a node outside src (it
// does, on some broken input), as the edits cut src at the nodes' positions.
func newTreeFilter(src string, file *syntax.File, dialect string, zshSource, bashTarget bool) (*treeFilter, *filterStop) {
	t := &treeFilter{src: src, file: file, dialect: dialect, zshSource: zshSource, zshOnly: zshSource && bashTarget,
		returnEnds: bashTarget, rejected: map[*syntax.Stmt]string{}}
	var err error
	top, at := 0, -1 // the top-level statement the walk is in starts at at
	syntax.Walk(file, func(n syntax.Node) bool {
		if n == nil { // Walk is done with a node
			return true
		}
		if s, ok := n.(*syntax.Stmt); ok && top < len(file.Stmts) && s == file.Stmts[top] {
			top, at = top+1, min(off(s.Pos()), len(src))
		}
		if off(n.Pos()) > len(src) || off(n.End()) > len(src) {
			err = fmt.Errorf("parsed positions past the end of the file (%s)", n.End())
		}
		if s, ok := n.(*syntax.Stmt); ok && off(stmtEnd(s)) < off(s.Pos()) {
			err = fmt.Errorf("a statement that ends before it starts (%s)", s.Pos())
		}
		if r, ok := n.(*syntax.Redirect); ok && r.Hdoc != nil {
			t.hdocs = append(t.hdocs, hdocSpan{off(r.OpPos), off(r.Hdoc.Pos()), off(r.Hdoc.End()), r})
		}
		return err == nil
	})
	if err != nil {
		return nil, &filterStop{at: at, reason: "the parser misplaced its statements: " + err.Error()}
	}
	return t, nil
}

func off(p syntax.Pos) int { return int(p.Offset()) }

// walk computes the edits afresh. It stops when they would cut a heredoc
// body out from under its operator, or when two of them overlap, which no
// rule should make.
func (t *treeFilter) walk() *filterStop {
	t.set.clear()
	t.stmts(t.file.Stmts, true, outcome{})
	t.stop(t.file.Stmts, scopeFile)
	if r := t.set.refused; r.err != nil {
		return &filterStop{at: r.at, line: lineOf(t.src, r.at), reason: "the filter's edits overlap: " + r.err.Error()}
	}
	if at := t.cutHeredoc(t.set.sorted()); at >= 0 {
		return &filterStop{at: at, line: lineOf(t.src, at), reason: "a skipped statement holds the body of an earlier heredoc"}
	}
	return nil
}

// stopScope is the list a stop is in, the file or a function's body: what
// a return or an exit in a guard there ends.
type stopScope int

const (
	scopeFile     stopScope = iota
	scopeFunction           // a { } body: an exit ends the shell
	scopeSubshell           // a ( ) body: an exit ends the function only
)

// guard is the statement of a list where zsh may stop reading it: one that
// holds a return or exit in a skip or drop of its own, or a call to a
// function that may end the shell (funcDef.exits), kept or not.
type guard struct {
	at    int    // its index in the list, -1 for none
	exits bool   // it may end the shell
	call  string // the function it calls, for a call
}

// reason returns why what follows g in scope is stopped.
func (g guard) reason(scope stopScope) string {
	what := "a skipped guard"
	if g.call != "" {
		what = "a call to " + g.call
	}
	switch {
	case g.exits && scope != scopeSubshell:
		return "stopped: " + what + " could end the shell here"
	case scope == scopeFile:
		return "stopped: " + what + " could end the file here"
	}
	return "stopped: " + what + " could return from the function here"
}

// stop handles the guards of list, the file or a function body, and
// reports whether there were any: the list may end early. A statement
// whose guards are returns nested in it returns there (returnsIn); at the
// first other guard, what follows it, up to the end of list, is commented
// out: the rest may be what it kept from running.
func (t *treeFilter) stop(list []*syntax.Stmt, scope stopScope) (ends bool) {
	for i := 0; i < len(list); {
		g := t.guardAt(list[i:])
		if g.at < 0 {
			break
		}
		i += g.at
		ends = true
		if !t.returnsIn(list[i]) {
			t.stopAt(list, i, g.reason(scope))
			break
		}
		// list[i] again: it may hold an exit guard too
	}
	return ends
}

// returnsIn makes each skip or drop nested in s that holds a return of the
// shell running s, and no exit, return where zsh may (edit.ret), and
// reports whether it made any: zsh's return there then is the filtered
// file's, on the paths that reach it, where a stop would also take away
// what follows on the paths that do not. s's own skip is left to the stop,
// which is exact for a statement of the list (it is reached whenever the
// list goes on to it) and keeps what never runs out of the report. So is
// a skip or drop in a pipeline or a background job: bash runs those in a
// subshell, where a return ends only the subshell, and zsh runs a
// pipeline's last command in the shell itself.
func (t *treeFilter) returnsIn(s *syntax.Stmt) bool {
	if !t.returnEnds || t.replaced(s) {
		return false
	}
	start, end := off(s.Pos()), off(stmtEnd(s))
	subshells := t.subshellRanges(s)
	made := false
	t.calls(s, func(n syntax.Node) bool {
		call, ok := n.(*syntax.CallExpr)
		if !ok || firstLit(call) != "return" {
			return true
		}
		e := t.set.holding(off(call.Pos()), start, end)
		if e == nil || e.returning != mayReturn || inRanges(subshells, e.start) || t.exitsIn(s, e.start, e.end) {
			return true
		}
		e.returnHere()
		made = true
		return true
	})
	return made
}

// subshellRanges returns the source ranges in s that bash runs in a
// subshell of the shell running s: the commands of a pipeline and the
// statements run in the background.
func (t *treeFilter) subshellRanges(s *syntax.Stmt) [][2]int {
	var ranges [][2]int
	syntax.Walk(s, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.Subshell, *syntax.CmdSubst, *syntax.ProcSubst, *syntax.FuncDecl:
			return false
		case *syntax.Stmt:
			if n.Background || n.Coprocess {
				ranges = append(ranges, [2]int{off(n.Pos()), off(stmtEnd(n))})
				return false
			}
		case *syntax.BinaryCmd:
			if n.Op == syntax.Pipe || n.Op == syntax.PipeAll {
				ranges = append(ranges, [2]int{off(n.X.Pos()), off(stmtEnd(n.Y))})
				return false
			}
		}
		return true
	})
	return ranges
}

func inRanges(ranges [][2]int, o int) bool {
	return slices.ContainsFunc(ranges, func(r [2]int) bool { return r[0] <= o && o < r[1] })
}

// exitsIn reports whether the source bytes [start, end) of s hold what may
// end the shell: an exit, a call to a function that may, or a definition of
// a function that holds an exit.
func (t *treeFilter) exitsIn(s *syntax.Stmt, start, end int) bool {
	found := false
	t.calls(s, func(n syntax.Node) bool {
		at := off(n.Pos())
		if at < start || at >= end {
			return true
		}
		switch n := n.(type) {
		case *syntax.FuncDecl:
			found = t.holdsExit(n.Body)
		case *syntax.CallExpr:
			name := firstLit(n)
			found = name == "exit" || t.set.def(name, at).exits
		}
		return !found
	})
	return found
}

// stopAt comments out what follows statement i of list, which holds a
// guard, for reason: the statements after it, and inside it what follows
// the guard (stopIn).
func (t *treeFilter) stopAt(list []*syntax.Stmt, i int, reason string) {
	t.stopIn(list[i], reason)
	if i < len(list)-1 {
		t.cutSkip(off(list[i+1].Pos()), off(stmtEnd(list[len(list)-1])), reason)
	}
}

// stopNested stops list, inside the statement that holds a guard, after
// the guard's statement, and reports whether list holds it.
func (t *treeFilter) stopNested(list []*syntax.Stmt, reason string) bool {
	g := t.guardAt(list)
	if g.at >= 0 {
		t.stopAt(list, g.at, reason)
	}
	return g.at >= 0
}

// stopIn comments out what follows the guard inside s, which holds it: the
// rest of the { } or branch body it is in, and the branches or case arms
// after that body. A guard in a test (an if or elif test) or a loop takes
// s whole: s would run what follows the guard, or run it again. Nothing in
// s follows a guard that is a skip or drop of s's own, or a call that is s.
func (t *treeFilter) stopIn(s *syntax.Stmt, reason string) {
	if t.replaced(s) {
		return
	}
	switch c := s.Cmd.(type) {
	case *syntax.Block:
		t.stopNested(c.Stmts, reason)
	case *syntax.BinaryCmd: // the right side runs after the left
		t.stopNested([]*syntax.Stmt{c.X, c.Y}, reason)
	case *syntax.TimeClause:
		t.stopNested([]*syntax.Stmt{c.Stmt}, reason)
	case *syntax.IfClause:
		for b := c; b != nil; b = b.Else {
			if t.guardAt(b.Cond).at >= 0 {
				t.cutSkip(off(s.Pos()), off(stmtEnd(s)), reason)
				return
			}
			if t.stopNested(b.Then, reason) {
				if b.Else != nil {
					t.cutDrop(s, off(b.Else.Position), off(c.FiPos), "", reason)
				}
				return
			}
		}
	case *syntax.CaseClause:
		for k, item := range c.Items {
			if t.stopNested(item.Stmts, reason) {
				if k < len(c.Items)-1 { // the arm now ends the case
					t.cutDrop(s, off(item.OpPos), off(c.Esac), ";;", reason)
				}
				return
			}
		}
	case *syntax.WhileClause, *syntax.ForClause:
		t.cutSkip(off(s.Pos()), off(stmtEnd(s)), reason)
	}
	// A simple command holds nothing that follows its guard.
}

// guardAt returns the first statement of list that holds a guard: a
// return or exit in a skip or drop of the statement's own (not one around
// the list, nor one that returns there, returnsIn), a call to a function
// that may end the shell, or such a skip of a function definition whose
// body holds an exit (bash has no such function: zsh may call it and
// exit).
func (t *treeFilter) guardAt(list []*syntax.Stmt) guard {
	for i, s := range list {
		start, end := off(s.Pos()), off(stmtEnd(s))
		g := guard{at: -1}
		t.calls(s, func(n syntax.Node) bool {
			switch n := n.(type) {
			case *syntax.FuncDecl:
				if t.set.removedIn(off(n.Pos()), start, end) && t.holdsExit(n.Body) {
					g = guard{at: i, exits: true}
				}
			case *syntax.CallExpr:
				name, at := firstLit(n), off(n.Pos())
				switch e := t.set.holding(at, start, end); {
				case (name == "return" || name == "exit") && e != nil && e.returning != returnsHere:
					g = guard{at: i, exits: name == "exit"}
				case t.set.def(name, at).exits:
					g = guard{at: i, exits: true, call: name}
				}
			}
			return g.at < 0
		})
		if g.at >= 0 {
			return g
		}
	}
	return guard{at: -1}
}

// calls calls fn with each command of s that runs in the shell running s
// (not in a ( ), $( ) or coproc) and each function s defines, whose body
// it does not walk, until fn returns false.
func (t *treeFilter) calls(s *syntax.Stmt, fn func(syntax.Node) bool) {
	more := true
	syntax.Walk(s, func(n syntax.Node) bool {
		switch n.(type) {
		case *syntax.Subshell, *syntax.CmdSubst, *syntax.ProcSubst, *syntax.CoprocClause:
			return false
		case *syntax.FuncDecl:
			more = more && fn(n)
			return false
		case *syntax.CallExpr:
			more = more && fn(n)
		}
		return more
	})
}

// cutSkip comments out the source bytes [start, end) for reason, with what
// walk made in them. The heredocs whose bodies follow end stay open on its
// ":", quoted, so their bodies stay text.
func (t *treeFilter) cutSkip(start, end int, reason string) {
	text := ":"
	for _, h := range t.hdocs {
		if h.op >= start && h.op < end && h.start >= end {
			text += " " + t.quotedHeredoc(h.r)
		}
	}
	t.set.cut(edit{kind: editSkip, start: start, end: end, text: text, reason: reason})
}

// cutDrop drops the branches of s from start to end, with what walk made
// in them, putting text there; or, when a heredoc body would be cut from
// its operator, s whole.
func (t *treeFilter) cutDrop(s *syntax.Stmt, start, end int, text, reason string) {
	if t.cutsBody(start, end) {
		t.cutSkip(off(s.Pos()), off(stmtEnd(s)), reason)
		return
	}
	t.set.cut(edit{kind: editDrop, start: start, end: end, text: text, reason: reason})
}

// firstLit returns the command name of call as written, or "".
func firstLit(call *syntax.CallExpr) string {
	if len(call.Args) == 0 {
		return ""
	}
	return call.Args[0].Lit()
}

// stmts filters a statement list and returns its outcome, that of its last
// statement; listed: it is a list of commands, not the inside of a word ($( )
// in an argument), whose aliases and exports the report counts. before is
// the outcome of the command that ran before the list, whose status $? holds
// for its first statement (each later one's is the statement before it): a
// statement that passes on that status holds that outcome.
func (t *treeFilter) stmts(list []*syntax.Stmt, listed bool, before outcome) outcome {
	o := before
	for _, s := range list {
		o = t.stmt(s, listed, o)
	}
	return o
}

// passesStatus reports whether s passes on the status of the statement
// before it: a return with no status, or a status that is not a number as
// written (return $r after local r=$?), or a command whose own words read
// $? (not those of the statements it holds, which read their own).
func (t *treeFilter) passesStatus(s *syntax.Stmt) bool {
	if call, ok := s.Cmd.(*syntax.CallExpr); ok && firstLit(call) == "return" {
		if len(call.Args) == 1 {
			return true
		}
		if _, err := strconv.Atoi(call.Args[1].Lit()); err != nil {
			return true
		}
	}
	reads := false
	t.ownNodes(s, func(n syntax.Node) bool {
		if p, ok := n.(*syntax.ParamExp); ok && p.Param != nil && p.Param.Value == "?" {
			reads = true
		}
		return !reads
	})
	return reads
}

// stmt filters s and returns its outcome. A compound whose test is not kept
// (if, elif, while, until, the case word, the for list, the left of && ||
// |) is skipped whole: none of its branches run, since the test that picks
// one does not, or not as written.
//
// before is the outcome of the command that ran before s (stmts).
func (t *treeFilter) stmt(s *syntax.Stmt, listed bool, before outcome) outcome {
	if s.Disown && t.zshOnly { // &! or &|: bash has &, and hash no jobs to disown
		at := off(s.Semicolon)
		t.set.add(edit{kind: editInsert, start: at, end: at + 2, text: "&"})
	}
	if export, ok := t.pathExport(s); ok && t.zshOnly && t.rejected[s] == "" {
		t.set.add(edit{kind: editInsert, start: off(s.Pos()), end: off(stmtEnd(s)), text: export})
		if listed {
			t.set.keep(off(s.Pos()), export)
		}
		return outcome{}
	}
	if test, ok := t.commandsTest(s); ok && t.zshSource && t.rejected[s] == "" {
		t.set.add(edit{kind: editInsert, start: off(s.Cmd.Pos()), end: off(s.Cmd.End()), text: test})
		return outcome{}
	}
	if reason := t.skipReason(s); reason != "" {
		return t.skip(s, reason)
	}
	m := t.set.mark()
	w, picks := t.words(s, before)
	if !picks.kept() {
		return t.escalate(s, m, picks)
	}
	o := t.command(s, listed, m, before).with(w)
	if t.passesStatus(s) {
		o = o.with(before)
	}
	return o
}

// command filters the commands s holds; s itself is not skipped, unless a
// test in it is. m is where the walk was before s, before the outcome of
// the command before it.
func (t *treeFilter) command(s *syntax.Stmt, listed bool, m mark, before outcome) outcome {
	switch c := s.Cmd.(type) {
	case *syntax.IfClause:
		return t.ifClause(s, c, m, before)
	case *syntax.WhileClause:
		o := t.tests(c.Cond, before)
		if !o.kept() {
			return t.escalate(s, m, o)
		}
		return t.loop(s, c.Do, c.DoPos, m, o)
	case *syntax.BinaryCmd:
		return t.binaryCmd(s, c, listed, m, before)
	case *syntax.CaseClause:
		return t.caseClause(s, c, m, before)
	case *syntax.ForClause:
		return t.loop(s, c.Do, c.DoPos, m, before)
	case *syntax.Block:
		o := t.body(c.Stmts, c.Lbrace, "{", before)
		t.closeBlock(c)
		return outcome{}.with(o)
	case *syntax.Subshell:
		return outcome{}.with(t.body(c.Stmts, c.Lparen, "(", before))
	case *syntax.FuncDecl:
		t.function(c) // defining it succeeds
	case *syntax.TimeClause:
		if c.Stmt != nil {
			return outcome{}.with(t.stmt(c.Stmt, listed, before))
		}
	case *syntax.CoprocClause:
		t.stmt(c.Stmt, listed, before) // starting it succeeds
	default: // a simple command: CallExpr, DeclClause, TestClause, ...
		if listed {
			t.set.keep(off(s.Pos()), t.text(s))
		}
		if call, ok := s.Cmd.(*syntax.CallExpr); ok {
			return t.set.def(firstLit(call), off(s.Pos())).call
		}
	}
	return outcome{}
}

// function filters the body of function c and records what a call to it
// is: whether it returns the status the source shell would give it, and
// whether it may end the shell where bash would not. A skipped guard that
// could return from it stops the body there, as one that could end the
// file stops the file. The body's first statement takes the status before
// a call as kept: the filter follows no status into a call.
func (t *treeFilter) function(c *syntax.FuncDecl) {
	o := t.stmt(c.Body, true, outcome{})
	var list []*syntax.Stmt
	scope := scopeFunction
	switch b := c.Body.Cmd.(type) {
	case *syntax.Block:
		list = b.Stmts
	case *syntax.Subshell:
		list, scope = b.Stmts, scopeSubshell
	}
	if t.replaced(c.Body) { // nothing in it is a guard of its own
		list = nil
	}
	if t.stop(list, scope) {
		o = o.with(outcome{status: stmtSkipped, reason: "a skipped guard could return from it"})
	}
	if c.Name == nil {
		return
	}
	d := funcDef{at: off(c.Pos()), name: c.Name.Value, exits: t.exits(c.Body)}
	if !o.kept() {
		d.call = outcome{status: stmtUnknown, reason: fmt.Sprintf("calls %s, whose status a skip decides (%s)", d.name, o.reason)}
	}
	t.set.define(d)
}

// exits reports whether body, a function's, may end the shell where bash
// would not: an exit in a skip, drop or stop of it, or a call to a function
// that may.
func (t *treeFilter) exits(body *syntax.Stmt) bool {
	return t.bodyExits(body, func(at int) bool { return removed(t.set.edits, at) })
}

// holdsExit reports whether body, a function's, holds an exit, or a call
// to a function that may end the shell.
func (t *treeFilter) holdsExit(body *syntax.Stmt) bool {
	return t.bodyExits(body, func(int) bool { return true })
}

// bodyExits reports whether body, a function's, is a { } body that holds
// an exit, or a definition of a function that holds one, for which counts
// reports true, or a call to a function that may end the shell. An exit in
// a ( ) body ends only the function.
func (t *treeFilter) bodyExits(body *syntax.Stmt, counts func(at int) bool) bool {
	if _, ok := body.Cmd.(*syntax.Block); !ok {
		return false
	}
	found := false
	t.calls(body, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.FuncDecl:
			found = counts(off(n.Pos())) && t.holdsExit(n.Body)
		case *syntax.CallExpr:
			name, at := firstLit(n), off(n.Pos())
			found = (name == "exit" && counts(at)) || t.set.def(name, at).exits
		}
		return !found
	})
	return found
}

// loop filters the body of loop s, which starts with do at pos and runs
// first after a command of outcome before. A break or continue that would
// leave s (or a loop around it) in a skip or drop made in s never runs: s
// could run forever, or run what it would jump over, so it is skipped
// whole.
func (t *treeFilter) loop(s *syntax.Stmt, body []*syntax.Stmt, pos syntax.Pos, m mark, before outcome) outcome {
	o := outcome{}.with(t.body(body, pos, "do", before))
	if t.jumpRemoved(s.Cmd, 0, t.set.since(m)) {
		return t.escalate(s, m, outcome{status: stmtSkipped, reason: "loop whose break or continue is skipped"})
	}
	return o
}

// jumpRemoved reports whether a break or continue in loop, depth loops
// inside the one checked, that leaves the one checked is in a skip or drop
// of edits. Those in a function, $( ) or ( ) leave no loop of loop's.
func (t *treeFilter) jumpRemoved(loop syntax.Node, depth int, edits []edit) bool {
	found := false
	syntax.Walk(loop, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.WhileClause, *syntax.ForClause:
			if n != loop {
				found = found || t.jumpRemoved(n, depth+1, edits)
				return false
			}
		case *syntax.FuncDecl, *syntax.CmdSubst, *syntax.ProcSubst, *syntax.Subshell:
			return false
		case *syntax.CallExpr:
			found = found || (jumpLevels(n) > depth && removed(edits, off(n.Pos())))
		}
		return !found
	})
	return found
}

// jumpLevels returns how many loops break or continue call leaves (break 2:
// two), or 0 for any other command.
func jumpLevels(call *syntax.CallExpr) int {
	if name := firstLit(call); name != "break" && name != "continue" {
		return 0
	}
	if len(call.Args) > 1 {
		if n, err := strconv.Atoi(call.Args[1].Lit()); err == nil {
			return n
		}
	}
	return 1
}

// binaryCmd filters x && y, x || y or x | y, which runs after a command of
// outcome before. A skipped x skips all of s; when x runs but its status is
// unknown, y is skipped, as whether it runs (or what it reads) depends on
// x. Either way s's status is unknown. The y of && and || reads x's status,
// that of | the one before s.
func (t *treeFilter) binaryCmd(s *syntax.Stmt, c *syntax.BinaryCmd, listed bool, m mark, before outcome) outcome {
	x := t.stmt(c.X, false, before)
	switch x.status {
	case stmtSkipped:
		return t.escalate(s, m, x)
	case stmtUnknown:
		t.skip(c.Y, x.reason)
		return x
	}
	if c.Op != syntax.Pipe && c.Op != syntax.PipeAll {
		before = x
	}
	return outcome{}.with(t.stmt(c.Y, listed, before))
}

// tests filters the statements whose status decides whether the rest of a
// compound runs, the first after a command of outcome before, up to the
// first one that is not kept, and returns its outcome.
func (t *treeFilter) tests(conds []*syntax.Stmt, before outcome) outcome {
	for _, c := range conds {
		if before = t.stmt(c, false, before); !before.kept() {
			return before
		}
	}
	return outcome{}
}

// escalate skips s whole, undoing the edits made in it since m, for what
// made o, the outcome of a test in it.
func (t *treeFilter) escalate(s *syntax.Stmt, m mark, o outcome) outcome {
	t.set.undo(m)
	return t.skip(s, o.reason)
}

// ifClause filters an if. An elif whose test is not kept drops itself and
// every branch after it (later elifs, else): which of them would run
// depends on its test. The if's status is unknown when a branch's last
// command is not kept, or branches are dropped.
func (t *treeFilter) ifClause(s *syntax.Stmt, c *syntax.IfClause, m mark, before outcome) outcome {
	if o := t.tests(c.Cond, before); !o.kept() {
		return t.escalate(s, m, o)
	}
	// Each body and elif test runs after a test, kept.
	o := outcome{}.with(t.body(c.Then, c.ThenPos, "then", outcome{}))
	for e := c.Else; e != nil; e = e.Else {
		if !e.ThenPos.IsValid() {
			o = o.with(t.body(e.Then, e.Position, "else", outcome{}))
			break
		}
		em := t.set.mark()
		if test := t.tests(e.Cond, outcome{}); !test.kept() {
			t.set.undo(em)
			if !t.drop(off(e.Position), off(c.FiPos), "", "else return; ", test.reason) {
				return t.escalate(s, m, test)
			}
			return o.with(test)
		}
		o = o.with(t.body(e.Then, e.ThenPos, "then", outcome{}))
	}
	return o
}

// body filters the commands of a compound's body, which starts with keyword
// at pos and runs after a command of outcome before, and returns its
// outcome. zsh takes an empty body, bash does not: it gets a ":".
func (t *treeFilter) body(list []*syntax.Stmt, pos syntax.Pos, keyword string, before outcome) outcome {
	if len(list) == 0 && t.zshOnly {
		at := off(pos) + len(keyword)
		t.set.add(edit{kind: editInsert, start: at, end: at, text: " :;"})
	}
	return t.stmts(list, true, before)
}

// caseClause filters a case. An arm whose pattern is skipped drops itself
// and every arm after it: a word it would have matched may match a later
// one. Unless the arm before falls through into it (;&): its body runs
// after that arm's, so only its pattern goes, and the arms after the end of
// the fallthrough chain are dropped, unless the chain ends in ;|, which
// goes on testing them either way. zsh's ;| becomes bash's ;;&. The
// case's status is unknown when an arm's last command is not kept, or arms
// are dropped.
//
// An arm's body runs after a command of outcome before, the one before s.
func (t *treeFilter) caseClause(s *syntax.Stmt, c *syntax.CaseClause, m mark, before outcome) outcome {
	var o, merged outcome // merged: an arm's pattern went, in this chain
	arm := before         // what the next arm's body runs after
	for i, item := range c.Items {
		if reason := t.patternsReason(item.Patterns); reason != "" {
			skipped := outcome{status: stmtSkipped, reason: reason}
			if i == 0 || c.Items[i-1].Op != syntax.Fallthrough {
				return t.dropArms(s, c, i, m, o.with(skipped))
			}
			if !t.mergeArm(c.Items[i-1], item, reason) {
				return t.escalate(s, m, skipped)
			}
			o, merged = o.with(skipped), skipped
		}
		body := t.stmts(item.Stmts, true, arm)
		o = o.with(body)
		switch item.Op { // the arm after it runs after its body, or after the test of its patterns
		case syntax.Fallthrough:
			arm = body
		case syntax.Resume, syntax.ResumeKorn:
			arm = before.with(body)
		default:
			arm = before
		}
		if item.Op == syntax.ResumeKorn && t.zshOnly { // zsh's ;| is bash's ;;&
			at := off(item.OpPos)
			t.set.add(edit{kind: editInsert, start: at, end: at + 2, text: ";;&"})
		}
		if item.Op == syntax.Fallthrough {
			continue
		}
		// The chain ends here. Unless it goes on testing the patterns after
		// it (;|), the arms after it run only when none of its patterns
		// matched, which a skipped one decides.
		if !merged.kept() && item.Op != syntax.ResumeKorn {
			return t.dropArms(s, c, i+1, m, o)
		}
		merged = outcome{}
	}
	return o
}

// dropArms drops the arms of c from arm i on, whose patterns depend on a
// skipped one, and returns o, the case's outcome; it skips s whole when
// they cannot be dropped.
func (t *treeFilter) dropArms(s *syntax.Stmt, c *syntax.CaseClause, i int, m mark, o outcome) outcome {
	if i == len(c.Items) {
		return o
	}
	if !t.drop(t.itemStart(c.Items[i]), off(c.Esac), "", "*) return;; ", o.reason) {
		return t.escalate(s, m, o)
	}
	return o
}

// mergeArm removes the ;& of arm prev and the patterns of item after it, so
// that item's body follows prev's. On one line, a ; separates the bodies.
func (t *treeFilter) mergeArm(prev, item *syntax.CaseItem, reason string) bool {
	start := off(prev.OpPos)
	end := off(item.Patterns[len(item.Patterns)-1].End())
	end += strings.IndexByte(t.src[end:], ')') + 1
	text := ""
	if n := len(prev.Stmts); n > 0 && !strings.Contains(t.src[start:end], "\n") &&
		stmtEnd(prev.Stmts[n-1]).Line() == prev.OpPos.Line() {
		text = ";"
	}
	return t.drop(start, end, text, "", reason)
}

// itemStart returns where a case arm starts: its first pattern, or the ( before it.
func (t *treeFilter) itemStart(item *syntax.CaseItem) int {
	start := off(item.Pos())
	before := strings.TrimRight(t.src[:start], " \t")
	if strings.HasSuffix(before, "(") {
		return len(before) - 1
	}
	return start
}

// closeBlock separates a block's last command from a } on its line: zsh
// ends the command there, bash reads the } as one more argument.
func (t *treeFilter) closeBlock(b *syntax.Block) {
	if !t.zshOnly || len(b.Stmts) == 0 {
		return
	}
	last := b.Stmts[len(b.Stmts)-1]
	if last.Semicolon.IsValid() || stmtEnd(last).Line() != b.Rbrace.Line() {
		return
	}
	at := off(stmtEnd(last))
	t.set.add(edit{kind: editInsert, start: at, end: at, text: ";"})
}

// words filters the statements inside s's own words ($( ) and <( ) in its
// arguments, redirections, for list or case word), not those of its body,
// puts its redirection operators in bash's spelling, and returns their
// outcome: unknown when any of them is not kept, as the
// words then hold other text. picks is the outcome of those in the words
// that pick what s runs: a case's word, a for's list; and before, the
// outcome of the command before s, when those words read $?.
func (t *treeFilter) words(s *syntax.Stmt, before outcome) (o, picks outcome) {
	picking := pickingWords(s)
	t.ownNodes(s, func(n syntax.Node) bool {
		var sub outcome
		switch n := n.(type) {
		case *syntax.CmdSubst:
			sub = t.subst(n.Stmts, before)
		case *syntax.ProcSubst:
			sub = t.subst(n.Stmts, before)
		case *syntax.Redirect:
			if e, ok := t.respellRedirect(n); ok && t.zshOnly {
				t.set.add(e)
			}
			return true
		case *syntax.ParamExp:
			if n.Param != nil && n.Param.Value == "?" && picking(n) {
				picks = picks.with(before)
			}
			return true
		default:
			return true
		}
		o = o.with(sub)
		if picking(n) {
			picks = picks.with(sub)
		}
		return false
	})
	return o, picks
}

// pickingWords returns whether a node of s is in the words that pick what
// s runs: a case's word, a for's list.
func pickingWords(s *syntax.Stmt) func(syntax.Node) bool {
	var from, to syntax.Node
	switch c := s.Cmd.(type) {
	case *syntax.CaseClause:
		from, to = c.Word, c.Word
	case *syntax.ForClause:
		if it, ok := c.Loop.(*syntax.WordIter); ok && len(it.Items) > 0 {
			from, to = it.Items[0], it.Items[len(it.Items)-1]
		}
	}
	return func(n syntax.Node) bool {
		return from != nil && off(from.Pos()) <= off(n.Pos()) && off(n.End()) <= off(to.End())
	}
}

// subst filters the commands of a $( ) or <( ), which runs after a command
// of outcome before, and returns their outcome: unknown when any of them is
// not kept.
func (t *treeFilter) subst(list []*syntax.Stmt, before outcome) outcome {
	var o outcome
	for _, s := range list {
		before = t.stmt(s, false, before)
		o = o.with(before)
	}
	return o
}

// ownNodes walks the nodes of s that belong to s itself: not the statements
// it holds (bodies, tests, the commands of a $( )) nor its case arms.
func (t *treeFilter) ownNodes(s *syntax.Stmt, fn func(syntax.Node) bool) {
	syntax.Walk(s, func(n syntax.Node) bool {
		switch n.(type) {
		case *syntax.Stmt:
			return n == s
		case *syntax.CaseItem:
			return false
		}
		return fn(n)
	})
}

// skipReason returns why s is skipped, or "": the target parser rejected
// it, a rule skips its command, or it uses zsh syntax bash cannot run.
func (t *treeFilter) skipReason(s *syntax.Stmt) string {
	if reason, ok := t.rejected[s]; ok {
		return reason
	}
	switch s.Cmd.(type) {
	case *syntax.CallExpr, *syntax.DeclClause, *syntax.TestClause, *syntax.ArithmCmd, *syntax.LetClause:
		// The command's own text: a negated one's starts after the "!".
		text := t.src[off(s.Cmd.Pos()):off(stmtEnd(s))]
		if reason := commandSkipReason(text, t.dialect); reason != "" {
			return reason
		}
		if t.foreign {
			if reason := execShellReason(text); reason != "" {
				return reason
			}
		}
	}
	if !t.zshOnly {
		return ""
	}
	if call, ok := s.Cmd.(*syntax.CallExpr); ok && len(call.Args) > 0 {
		if reason, ok := zshBuiltins[call.Args[0].Lit()]; ok {
			return reason
		}
	}
	var reason string
	t.ownNodes(s, func(n syntax.Node) bool {
		switch n.(type) {
		case *syntax.CmdSubst, *syntax.ProcSubst:
			return false // their commands are statements of their own
		}
		if reason == "" { // Walk goes on to the siblings of a node it stops at
			reason = zshOnlyReason(n)
		}
		return reason == ""
	})
	return reason
}

// patternsReason returns why a case arm with patterns is skipped, or "".
func (t *treeFilter) patternsReason(patterns []*syntax.Word) string {
	if !t.zshOnly {
		return ""
	}
	var reason string
	for _, w := range patterns {
		syntax.Walk(w, func(n syntax.Node) bool {
			if reason == "" {
				reason = zshOnlyReason(n)
			}
			return reason == ""
		})
		if reason != "" {
			return reason
		}
	}
	return ""
}

// zshOnlyReason returns why node n is zsh syntax bash has not, or reads as
// something else, or "".
func zshOnlyReason(n syntax.Node) string {
	switch n := n.(type) {
	case *syntax.ParamExp:
		return zshParamReason(n)
	case *syntax.Word:
		for _, part := range n.Parts {
			if lit, ok := part.(*syntax.Lit); ok && strings.HasPrefix(lit.Value, "(") {
				return "zsh glob flags or qualifiers"
			}
		}
	case *syntax.FuncDecl:
		if n.Name == nil {
			return "zsh function with no name or several names"
		}
	case *syntax.Assign:
		return zshAssignReason(n)
	}
	return ""
}

// zshParamReason returns why parameter expansion p is zsh's own, or "":
// flags, ${+x}, ${=x}, ${~x}, ${^x}, a nested expansion, :t-style
// modifiers, or $x[i], which bash reads as $x and the text [i].
func zshParamReason(p *syntax.ParamExp) string {
	switch {
	case p.Flags != nil:
		return "zsh parameter expansion flags"
	case p.NestedParam != nil || p.IsSet || p.Split != 0 || p.GlobSubst != 0 || p.RcExpand != 0 ||
		len(p.Modifiers) > 0 || (p.Short && p.Index != nil):
		return "zsh parameter expansion"
	case zshIndex(p.Index):
		return "zsh array index (zsh counts from 1)"
	}
	return ""
}

// commandSkipReason returns why a simple command, text as written, is
// skipped by the rules, or "".
func commandSkipReason(text, dialect string) string {
	if reason := shouldSkipLine(text, dialect); reason != "" {
		return reason
	}
	if reason, ok := noopBuiltins[firstWord(text)]; ok {
		return reason
	}
	if reason := aliasSkipReason(text); reason != "" {
		return reason
	}
	return typesetSkipReason(text)
}

// text returns s as written, without its heredoc bodies and separator.
func (t *treeFilter) text(s *syntax.Stmt) string {
	return t.src[off(s.Pos()):off(stmtEnd(s))]
}

// stmtEnd returns where s ends without the ; or & after it and without its
// heredoc bodies, which follow the line their operator is on.
func stmtEnd(s *syntax.Stmt) syntax.Pos {
	end := s.Position
	if s.Cmd != nil {
		end = cmdEnd(s.Cmd)
	} else if s.Negated {
		end = syntax.NewPos(s.Position.Offset()+1, s.Position.Line(), s.Position.Col()+1)
	}
	for _, r := range s.Redirs {
		if r.Word != nil && r.Word.End().After(end) {
			end = r.Word.End()
		}
	}
	return end
}

func cmdEnd(c syntax.Command) syntax.Pos {
	switch c := c.(type) {
	case *syntax.BinaryCmd:
		return stmtEnd(c.Y)
	case *syntax.FuncDecl:
		return stmtEnd(c.Body)
	case *syntax.TimeClause:
		if c.Stmt != nil {
			return stmtEnd(c.Stmt)
		}
	case *syntax.CoprocClause:
		return stmtEnd(c.Stmt)
	}
	return c.End()
}

// skip replaces s with ":" and returns its outcome. Heredocs whose bodies
// follow s's last line stay open on the ":", with their delimiters quoted, so
// their bodies stay text.
func (t *treeFilter) skip(s *syntax.Stmt, reason string) outcome {
	start, end := off(s.Pos()), off(stmtEnd(s))
	text := ":"
	endLine := stmtEnd(s).Line()
	syntax.Walk(s, func(n syntax.Node) bool {
		r, ok := n.(*syntax.Redirect)
		if !ok || (r.Op != syntax.Hdoc && r.Op != syntax.DashHdoc) {
			return true
		}
		if (r.Hdoc != nil && off(r.Hdoc.Pos()) >= end) || (r.Hdoc == nil && r.OpPos.Line() == endLine) {
			text += " " + t.quotedHeredoc(r)
		}
		return true
	})
	ret := "return" + strings.TrimPrefix(text, ":")
	t.set.add(newSkipOrDrop(editSkip, start, end, text, ret, reason))
	return outcome{stmtSkipped, reason}
}

// quotedHeredoc returns heredoc operator r with its delimiter quoted.
func (t *treeFilter) quotedHeredoc(r *syntax.Redirect) string {
	word := t.src[off(r.Word.Pos()):off(r.Word.End())]
	if lit := r.Word.Lit(); lit != "" && !strings.ContainsAny(lit, `\'`) {
		word = "'" + lit + "'"
	}
	return r.Op.String() + word
}

// drop replaces the branches from start to end (an elif to its fi, case
// arms to their esac, a ;& and the patterns after it) with text, or ret,
// the branch that returns where they may (edit.ret). It reports false when
// a heredoc in them has its body after end, which only skipping the whole
// compound keeps text.
func (t *treeFilter) drop(start, end int, text, ret, reason string) bool {
	if t.cutsBody(start, end) {
		return false
	}
	t.set.add(newSkipOrDrop(editDrop, start, end, text, ret, reason))
	return true
}

// cutsBody reports whether a heredoc whose operator is in the source bytes
// [start, end) has its body after end.
func (t *treeFilter) cutsBody(start, end int) bool {
	return slices.ContainsFunc(t.hdocs, func(h hdocSpan) bool { return h.op >= start && h.op < end && h.start >= end })
}

// cutHeredoc returns the offset of a heredoc's operator when one of edits
// would cut its body out from under it, the operator being before the
// edit: the edit takes part of the body and code outside it. One all
// inside the body is in a $( ) there, code. It returns -1 for none.
func (t *treeFilter) cutHeredoc(edits []edit) int {
	for _, e := range edits {
		for _, h := range t.hdocs {
			inside := h.start <= e.start && e.end <= h.end
			if h.op < e.start && h.start < e.end && h.end > e.start && !inside {
				return h.op
			}
		}
	}
	return -1
}

// outSpan is where an edit's text landed in the output, and the source
// bytes it replaced.
type outSpan struct{ start, end, srcStart, srcEnd int }

// render returns the source with the edits made, line for line: a skip that
// spans lines leaves its lines as comments and its ":" on its last line, so
// every other line keeps its number. A skip with nothing after it on its
// line gets the source as a comment after its ":". The edits are disjoint
// and in source order (walk's editSet and sort).
func (t *treeFilter) render() (string, []outSpan) {
	edits := t.set.edits
	var b strings.Builder
	b.Grow(len(t.src) + 32*len(edits))
	spans := make([]outSpan, 0, len(edits))
	pos := 0
	for _, e := range edits {
		b.WriteString(t.src[pos:e.start])
		span := outSpan{start: b.Len(), srcStart: e.start, srcEnd: e.end}
		t.renderEdit(&b, e)
		span.end = b.Len()
		spans = append(spans, span)
		pos = e.end
	}
	b.WriteString(t.src[pos:])
	return b.String(), spans
}

func (t *treeFilter) renderEdit(b *strings.Builder, e edit) {
	lines := strings.Split(t.src[e.start:e.end], "\n")
	if len(lines) > 1 {
		lineStart := strings.LastIndexByte(t.src[:e.start], '\n') + 1
		if strings.TrimSpace(t.src[lineStart:e.start]) != "" {
			b.WriteByte(' ')
		}
		for i, line := range lines[:len(lines)-1] {
			if i > 0 {
				b.WriteString(indent(line))
			}
			if line = strings.TrimSpace(line); line != "" {
				b.WriteString(skipComment + line)
			}
			b.WriteByte('\n')
		}
		b.WriteString(indent(lines[len(lines)-1]))
	}
	b.WriteString(e.text)
	rest, _, _ := strings.Cut(t.src[e.end:], "\n")
	if e.kind == editSkip && strings.TrimSpace(rest) == "" {
		b.WriteString(" " + skipComment + strings.TrimSpace(lines[len(lines)-1]))
	}
}

func indent(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

// sourceOffset returns the source offset of the place in the rendered
// output that the parse error err points at; one inside an edit's text is
// the edit's start. It is -1 when err has no position.
func sourceOffset(spans []outSpan, err error) int {
	o := parseErrorOffset(err)
	if o < 0 {
		return -1
	}
	delta := 0 // source offset minus output offset after the edits before o
	for _, s := range spans {
		switch {
		case o < s.start:
			return o + delta
		case o < s.end:
			return s.srcStart
		}
		delta = s.srcEnd - s.end
	}
	return o + delta
}

// stmtAt returns the innermost statement at source offset o that no edit
// replaced, or nil.
func (t *treeFilter) stmtAt(o int) *syntax.Stmt {
	if o < 0 {
		return nil
	}
	var at *syntax.Stmt
	syntax.Walk(t.file, func(n syntax.Node) bool {
		s, ok := n.(*syntax.Stmt)
		if ok && off(s.Pos()) <= o && o <= off(s.End()) && !t.replaced(s) {
			at = s
		}
		return true
	})
	return at
}

// replaced reports whether an edit replaced all of s.
func (t *treeFilter) replaced(s *syntax.Stmt) bool {
	return t.set.replaced(off(s.Pos()), off(stmtEnd(s)))
}

// report fills a fresh report with the skips, the aliases and exports kept,
// and the functions prog, the output, defines.
func (t *treeFilter) report(newReport func() *Report, prog *syntax.File) *Report {
	report := newReport()
	for _, e := range t.set.edits {
		if e.reported() {
			first, _, _ := strings.Cut(t.src[e.start:e.end], "\n")
			report.AddSkipped(lineOf(t.src, e.start), strings.TrimSpace(first), e.reason)
		}
	}
	for _, im := range t.set.imports {
		trackImport(report, strings.TrimSpace(im.text))
	}
	addFunctions(report, prog)
	return report
}

func lineOf(src string, o int) int { return strings.Count(src[:o], "\n") + 1 }

// maxRepairs bounds how many statements the filter replaces for not parsing
// in the target before it stops at the first of them.
const maxRepairs = 100

// addFunctions records the functions prog defines in report.
func addFunctions(report *Report, prog *syntax.File) {
	if prog == nil {
		return
	}
	syntax.Walk(prog, func(node syntax.Node) bool {
		if fn, ok := node.(*syntax.FuncDecl); ok && fn.Name != nil { // zsh's () { } has none
			report.AddImported(ItemFunction, fn.Name.Value, "")
		}
		return true
	})
}
