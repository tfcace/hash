package completion

import (
	"cmp"
	"fmt"
	"io"
	"io/fs"
	"math"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/tfcace/hash/internal/shlex"
	"github.com/tfcace/hash/internal/shparse"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/pattern"
	"mvdan.cc/sh/v3/syntax"
)

// insertionCheck reads each completed line back with hash's own parser
// (mvdan's, in the dialect the executor runs) before its item is offered:
// a value is held to it as a valueCheck, a variable reference as a
// refCheck.
// Escaping is the first line of defense; this check makes a wrong escape
// lose an item instead of running code.
//
// A completed line is held against a reference: the same line with a
// placeholder value the same place wrote. Both must parse the same way
// (whole, or recovered at the same places, with the same closer after
// them), have the same commands, redirections, substitutions, heredocs and
// comments outside the insertion and nothing but quoting inside it, and the
// word the insertion is in must expand as the reference's does with the
// placeholder replaced by the value. A line whose reference does not parse
// is not checked into anything: nothing is offered.
//
// Hash's parser reads the reference line whole once, for where its
// top-level statements start and end; then the reference and each item
// are read only in the statement the insertion is in and the ones right
// before and after it (scopeOf). The statements before those read the
// same whatever is inserted after them, and the statement after the
// insertion's shows any change in how that one ends.
//
// The guarantee is hash's parse of the line, and stops there: what a
// command does with the word it gets is its own. Where a command is known
// to parse the word again as code (codeReaders: eval, alias, trap, a
// shell's -c, su, script and flock -c or --command, watch, after command,
// sudo, env, timeout, nice, flock, script and their options, and a
// shell's or su's -c after a runner it does not know, as in xargs sh -c)
// only plain words are held; a command that runs it otherwise (ssh's
// remote command, sudo -s, eval after an option of a runner it does not
// know, a heredoc fed to a shell, output another eval reads) is out of its
// reach.
type insertionCheck struct {
	parser *syntax.Parser
	// line is the scope the lines are read in (scopeOf), with the replaced
	// text line[from:pos].
	line      string
	from, pos int
	// end is set when the scope runs to the end of the line: what closes
	// what the reference leaves open there. Nil: the scope ends inside the
	// line, before a statement after the insertion's, and nothing closes.
	end *closedEnd
	ref *readLine
}

// closedEnd is the end of the line, where closer closes what a line read
// there leaves open (closers).
type closedEnd struct{ closer string }

// checker holds the text of an item to what the item should mean: a
// valueCheck, a refCheck, or a refusal where no reference line could be
// read.
type checker interface {
	holds(text, meaning string) (ok bool, why string)
}

// reading is how hash's parser reads a place with the placeholder written
// there: an insertionCheck, or a refusal.
type reading interface {
	// forValues is the checker for values whose reference line has
	// refLine at the place, written as the writer means it to be read
	// (reads).
	forValues(refLine string, reads []role) checker
}

// valueCheck holds a value written for the place: the word it is in must
// expand as the reference's (words) does, with the value for the
// placeholder.
type valueCheck struct {
	*insertionCheck
	words expansion
}

// refCheck holds a variable reference (env's $NAME), meant to expand: the
// nodes of the word it is in are compared, the name left out.
type refCheck struct{ *insertionCheck }

// refusal holds nothing: it says why no value is offered at a place.
type refusal string

func (r refusal) holds(string, string) (ok bool, why string) { return false, string(r) }
func (r refusal) forValues(string, []role) checker           { return r }

// placeholder stands for a value in the reference line: plain letters,
// which mean themselves wherever a value goes.
const placeholder = "hashQvalue"

// readLine is a completed line as the parser read it, for comparison.
type readLine struct {
	src       string
	from, end int           // the insertion is src[from:end]
	nodes     []syntax.Node // the nodes outside the insertion's word, in walk order
	target    target        // the word (or comment) the insertion is in
	word      *syntax.Word  // the word, or nil
	// reread: a word around the insertion is code a command parses again
	// (an argument of eval).
	reread bool
}

// target is the word, or comment, the insertion is in: how it is
// expanded and where it is (a comment's start only).
type target struct {
	role     role
	from, to mappedPos
}

func (t target) String() string { return fmt.Sprintf("%s %s-%s", t.role, t.from, t.to) }

// mappedPos is a position in one of two lines that differ only in their
// insertions, the same in both where the lines read the same: before the
// insertion as it is, inside it from its start, after it from its end.
type mappedPos struct {
	zone posZone
	off  int
}

type posZone uint8

const (
	zoneInvalid   posZone = iota // no position
	zoneRecovered                // where the parser recovered a missing token
	zoneBefore
	zoneInside
	zoneAfter
)

func (p mappedPos) String() string {
	return fmt.Sprintf("%c%d", "-Rbia"[p.zone], p.off)
}

// expansion is what the insertion's word expands to: not expanded (a
// comment, shell text), its fields, or why expanding it failed.
type expansion struct {
	state  expansionState
	fields []string // expanded: none for a word that expands to nothing
	err    string   // expansionFailed
}

type expansionState uint8

const (
	notExpanded expansionState = iota
	expanded
	expansionFailed
)

func expandedTo(fields ...string) expansion {
	return expansion{state: expanded, fields: fields}
}

func expansionError(err string) expansion {
	return expansion{state: expansionFailed, err: err}
}

// readPlace reads the reference line: line with line[from:pos] replaced
// by refLine, the placeholder written for the place.
func readPlace(lang syntax.LangVariant, line string, from, pos int, refLine string) reading {
	c, err := readReference(lang, line, from, pos, refLine)
	if err != nil {
		return refusal("the line does not parse: " + err.Error())
	}
	return c
}

// newRefCheck is the checker for variable references in place of
// line[from:pos]: refLine is the sigil and a placeholder name.
func newRefCheck(lang syntax.LangVariant, line string, from, pos int, refLine string) checker {
	c, err := readReference(lang, line, from, pos, refLine)
	if err != nil {
		return refusal("the line does not parse: " + err.Error())
	}
	return refCheck{c}
}

// readReference reads the reference line: whole (readWhole), to find the
// statements the insertion is in (scopeOf), and then in them, where the
// items' lines are read too. A reference that does not read in its scope
// is read whole.
func readReference(lang syntax.LangVariant, line string, from, pos int, refLine string) (*insertionCheck, error) {
	whole, file, err := readWhole(lang, line, from, pos, refLine)
	if err != nil {
		return nil, err
	}
	refEnd, size := from+len(refLine), len(line)-pos+from+len(refLine)
	if start, end := scopeOf(file, from, refEnd); start > 0 || end < size {
		end = min(end, size)
		c := &insertionCheck{parser: whole.parser, line: line[start : end-refEnd+pos], from: from - start, pos: pos - start}
		if end == size {
			c.end = whole.end
		}
		if c.ref, err = c.read(refLine); err == nil {
			return c, nil
		}
	}
	whole.ref, err = whole.read(refLine)
	return whole, err
}

// readWhole parses the reference line, line with line[from:pos] replaced
// by refLine, whole: with the first closer it parses with, which the
// check's lines are read with.
func readWhole(lang syntax.LangVariant, line string, from, pos int, refLine string) (*insertionCheck, *syntax.File, error) {
	parser := syntax.NewParser(syntax.Variant(lang), syntax.KeepComments(true), syntax.RecoverErrors(8))
	src := line[:from] + refLine + line[pos:]
	var lex shlex.Lexer
	lex.Scan(src)
	var err error
	for _, closer := range closers(lex.Closer()) {
		var file *syntax.File
		if file, err = shparse.Parse(parser, src+closer, ""); err == nil {
			return &insertionCheck{parser: parser, line: line, from: from, pos: pos, end: &closedEnd{closer}}, file, nil
		}
	}
	return nil, nil, err
}

// closers are the texts tried in turn after a line to close what it
// leaves open. The lexer's closer ends the quotes, substitutions and
// heredocs (the parser's recovery would lose an unclosed quote's text);
// where the lexer misreads the line (an extglob it takes for a group and a
// comment) it is wrong, and the line goes without one. The lexer knows no
// reserved words, and the parser's recovery does not end a [[ test, a case
// pattern or a for (( loop's head: the grammar's closers do, after the
// lexer's or, in the loop's head, before it.
func closers(lexed string) []string {
	closers := []string{lexed}
	if lexed != "" {
		closers = append(closers, "")
	}
	return append(closers, lexed+" ]]", lexed+")", ";"+lexed, ";;"+lexed)
}

// scopeOf is the part [start, end) of a line, read whole as file, that the
// lines of an insertion at [from, to) are read in: the top-level statement
// the insertion is in, the one before it and the one after it, or up to
// the line's start or end. What follows a complete statement does not
// change how it reads; the statements around the insertion's are read
// too, as a value that escapes the newline after it, or opens a quote or a
// heredoc, changes how the next one reads, and one that starts with an
// operator may join the one before (& and ! to zsh's &!, across a NUL
// hash's parser skips). A statement with a recovered position (a token
// hash's parser made up) reaches the end: what comes after it is read with
// less recovery left.
func scopeOf(file *syntax.File, from, to int) (start, end int) {
	end = math.MaxInt
	defer func() {
		if recover() != nil { // mvdan's nodes of a recovered parse may panic for their position
			start, end = 0, math.MaxInt
		}
	}()
	var cuts []int // where no statement goes on: after one, before the next
	reach := 0     // how far the statements so far reach
	for i, s := range file.Stmts {
		lo, hi := extent(s)
		if i > 0 && reach <= lo {
			cuts = append(cuts, reach)
		}
		reach = max(reach, hi)
	}
	if reach < math.MaxInt {
		cuts = append(cuts, reach)
	}
	before := slices.IndexFunc(cuts, func(cut int) bool { return cut >= from }) // the first cut not before the insertion
	if before < 0 {
		before = len(cuts)
	}
	if before >= 2 {
		start = cuts[before-2]
	}
	if after := slices.IndexFunc(cuts, func(cut int) bool { return cut >= to }); after >= 0 && after+1 < len(cuts) {
		end = cuts[after+1]
	}
	return start, end
}

// extent is where statement s, its comments and heredoc bodies included,
// starts and ends; a statement with a recovered position ends nowhere.
func extent(s *syntax.Stmt) (lo, hi int) {
	lo = math.MaxInt
	add := func(n syntax.Node) {
		if p := n.Pos(); p.IsValid() {
			lo = min(lo, int(p.Offset()))
		}
		switch e := n.End(); {
		case e.IsRecovered(), n.Pos().IsRecovered():
			hi = math.MaxInt
		case e.IsValid():
			hi = max(hi, int(e.Offset()))
		}
	}
	for i := range s.Comments {
		add(&s.Comments[i])
	}
	syntax.Walk(s, func(n syntax.Node) bool {
		if n != nil {
			add(n)
		}
		return n != nil
	})
	return lo, hi
}

// forValues reads refLine in c's scope, with the closer it parses with
// where the scope runs to the line's end. Where it does not, the scope
// ends where c's reference has a statement after the insertion's, so
// refLine must read as c's reference does outside the insertion.
func (c *insertionCheck) forValues(refLine string, reads []role) (check checker) {
	defer func() {
		if r := recover(); r != nil {
			check = refusal(unplaced(r).Error())
		}
	}()
	if refLine == c.ref.src[c.ref.from:c.ref.end] {
		return c.heldTo(reads)
	}
	d := *c
	var err error
	if d.end == nil {
		d.ref, err = d.read(refLine)
	} else {
		var lex shlex.Lexer
		lex.Scan(d.line[:d.from])
		lex.Scan(refLine)
		lex.Scan(d.line[d.pos:])
		for _, closer := range closers(lex.Closer()) {
			d.end = &closedEnd{closer}
			if d.ref, err = d.read(refLine); err == nil {
				break
			}
		}
	}
	switch {
	case err != nil:
		return refusal("the line does not parse: " + err.Error())
	case d.end == nil && (d.ref.target != c.ref.target || !sameNodes(d.ref, d.ref.nodes, c.ref, c.ref.nodes, false)):
		return refusal("the place reads otherwise with the value's own head or tail")
	}
	return d.heldTo(reads)
}

// heldTo is the value check of c's reference, or a refusal where it is not
// read the way the place's writer means (reads: a body writer's text must
// land in a body).
func (c *insertionCheck) heldTo(reads []role) checker {
	ref := c.ref
	words := ref.expansion()
	switch {
	case ref.foreign():
		return refusal("the value would be part of an expansion")
	case !slices.Contains(reads, ref.target.role):
		return refusal("hash's parser reads the place as " + ref.target.String())
	case words.state == expansionFailed:
		return refusal("the word does not expand: " + words.err)
	case words.state == expanded && strings.Count(strings.Join(words.fields, "\x00"), placeholder) != 1:
		return refusal("the word's expansion does not keep the value as it is")
	}
	return valueCheck{c, words}
}

// holds reports whether text reads back as the reference with meaning in
// place of the placeholder, with nothing else changed, or why not.
func (c valueCheck) holds(text, meaning string) (ok bool, why string) {
	defer refuseUnplaced(&ok, &why)
	if c.ref.reread && strings.TrimFunc(text, plainWordRune) != "" {
		return false, "a command parses the word again as code, where only a plain word means itself"
	}
	got, why := c.readItem(text)
	switch {
	case got == nil:
		return false, why
	case got.foreign():
		return false, "the value holds an expansion or a command"
	case !sameNodes(got, got.nodes, c.ref, c.ref.nodes, false):
		return false, "the line reads differently outside the value"
	case c.words.state == expanded:
		return c.expandsAs(got.expansion(), meaning)
	}
	return true, ""
}

// holds reports whether text reads back as the reference does, but for the
// name it references.
func (c refCheck) holds(text, _ string) (ok bool, why string) {
	defer refuseUnplaced(&ok, &why)
	got, why := c.readItem(text)
	switch {
	case got == nil:
		return false, why
	case !sameNodes(got, got.nodes, c.ref, c.ref.nodes, false) || !sameNodes(got, wordNodes(got.word), c.ref, wordNodes(c.ref.word), true):
		return false, "the line reads differently outside the name"
	}
	return true, ""
}

// readItem reads an item's text in c's scope, with the insertion in the
// reference's word, or says why not.
func (c *insertionCheck) readItem(text string) (got *readLine, why string) {
	got, err := c.read(text)
	switch {
	case err != nil:
		return nil, "the completed line does not parse: " + err.Error()
	case got.target != c.ref.target:
		return nil, fmt.Sprintf("the value is not in one word (%s, want %s)", got.target, c.ref.target)
	}
	return got, ""
}

// expandsAs reports whether words is the reference's expansion with
// meaning in place of the placeholder, or why not.
func (c valueCheck) expandsAs(words expansion, meaning string) (ok bool, why string) {
	if words.state != expanded {
		return false, "the word does not expand: " + words.err
	}
	if c.ref.target.role == rolePattern {
		if !utf8.ValidString(meaning) {
			return false, "hash's interpreter fails on a pattern that is not UTF-8"
		}
		meaning = backslashBefore(meaning, patternSpecial)
	}
	want := make([]string, len(c.words.fields))
	for i, field := range c.words.fields {
		want[i] = strings.Replace(field, placeholder, meaning, 1)
	}
	if c.ref.target.role == rolePattern && !sameMatch(words.fields[0], want[0]) {
		return false, fmt.Sprintf("the pattern %q matches otherwise than %q", words.fields[0], want[0])
	}
	if c.ref.target.role != rolePattern && !slices.Equal(words.fields, want) {
		return false, fmt.Sprintf("the word expands to %q, want %q", words.fields, want)
	}
	return true, ""
}

// patternSpecial are the bytes a backslash makes literal in a pattern
// that may be special in one: every ASCII punctuation byte.
const patternSpecial = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"

// sameMatch reports whether patterns p and q match the same strings, the
// way hash's interpreter matches a case or [[ pattern (a\ b as a b).
func sameMatch(p, q string) bool {
	mode := pattern.EntireString | pattern.ExtendedOperators
	pre, err := pattern.Regexp(p, mode)
	if err != nil {
		return false
	}
	qre, err := pattern.Regexp(q, mode)
	return err == nil && pre == qre
}

// role is the way the word an insertion is in is expanded, by where it
// is: what a place's writer means its text to be read as.
type role uint8

const (
	noTarget     role = iota // the insertion is in no word or comment
	roleFields               // an argument: split into fields, globbed, braces expanded
	roleLiteral              // a value, a redirection's target: one string
	roleDocument             // a heredoc body
	roleRaw                  // the body of a heredoc with a quoted delimiter: its text as it is
	rolePattern              // a case or [[ == ]] pattern
	roleArith                // an arithmetic expression's operand, which no writer writes
	roleComment
)

var roleNames = [...]string{"no word", "fields", "literal", "document", "raw", "pattern", "arith", "comment"}

func (r role) String() string { return roleNames[r] }

// read parses c's line with text in the insertion, after it the closer,
// and describes it.
func (c *insertionCheck) read(text string) (r *readLine, err error) {
	defer func() {
		if p := recover(); p != nil {
			r, err = nil, unplaced(p)
		}
	}()
	src := c.line[:c.from] + text + c.line[c.pos:]
	if c.end != nil {
		src += c.end.closer
	}
	file, err := shparse.Parse(c.parser, src, "")
	if err != nil {
		return nil, err
	}
	r = &readLine{src: src, from: c.from, end: c.from + len(text)}
	h := r.holder(file)
	syntax.Walk(file, func(n syntax.Node) bool {
		switch {
		case n == nil:
			return false
		case h.held != nil && h.held.is(n):
			r.target = target{role: roleComment, from: r.at(n.Pos())}
			if w, ok := n.(*syntax.Word); ok {
				r.target.role, r.target.to, r.word = h.roles.of(w), r.at(n.End()), w
			}
			return false // the word's nodes are its checker's
		}
		r.nodes = append(r.nodes, n)
		return true
	})
	r.reread = h.reread
	return r, nil
}

// refuseUnplaced, deferred, refuses an item whose line has a node that
// panics for its position (unplaced).
func refuseUnplaced(ok *bool, why *string) {
	if r := recover(); r != nil {
		*ok, *why = false, unplaced(r).Error()
	}
}

// unplaced is the error of a panic placing a node: mvdan's nodes of a
// recovered parse may panic for their position (a case item with no
// pattern, from `case (`). The line is refused; completion must not end the
// user's shell, and the router writes items on its caller's goroutine.
func unplaced(p any) error {
	return fmt.Errorf("hash's parser left a node without a position: %v", p)
}

// holder is what holds the insertion, and the roles of the line's words.
type holder struct {
	held   held // nil: nothing holds it
	roles  wordRoleMap
	reread bool // a word around the insertion is code a command parses again
}

// held is the node that holds the insertion: the innermost word that holds
// all of it (heldWord), or else the comment it is in (heldComment).
type held interface {
	is(n syntax.Node) bool
}

type heldWord struct{ word *syntax.Word }

// heldComment is a comment by the position of its #: walking a file gives
// its comments at new addresses each time.
type heldComment struct{ hash syntax.Pos }

func (h heldWord) is(n syntax.Node) bool { return n == syntax.Node(h.word) }

func (h heldComment) is(n syntax.Node) bool {
	cm, ok := n.(*syntax.Comment)
	return ok && cm.Pos() == h.hash
}

// holder finds the innermost word, or else the comment, that holds all of
// the insertion (a node recovered at the end of src holds the rest).
func (r *readLine) holder(file *syntax.File) holder {
	h, span := holder{roles: wordRoleMap{}}, -1
	var word, comment held
	var substs []*syntax.CmdSubst // the substitutions walked into, outermost first
	syntax.Walk(file, func(n syntax.Node) bool {
		h.roles.visit(n)
		switch n := n.(type) {
		case nil:
			return false
		case *syntax.CmdSubst:
			substs = append(substs, n)
		case *syntax.Comment:
			if r.inComment(n, substs) {
				comment = heldComment{n.Pos()} // inside any word around the comment's command
			}
		case *syntax.Word:
			if size, ok := r.spans(n); ok && (span < 0 || size <= span) { // the later is inside
				word, span = heldWord{n}, size
			}
		case *syntax.CallExpr:
			for i, arg := range n.Args {
				_, ok := r.spans(arg)
				h.reread = h.reread || ok && rereads(n.Args, i)
			}
		}
		return true
	})
	h.held = cmp.Or(comment, word)
	return h
}

// inComment reports whether the insertion is in comment cm, inside
// substs. A comment's End counts its text, which inside backticks has lost
// the backslashes they take off: the insertion is in it when it is on the
// comment's line after the #, before the end of a substitution around it
// (a backtick ends it on that line).
func (r *readLine) inComment(cm *syntax.Comment, substs []*syntax.CmdSubst) bool {
	hash := offset(r.src, cm.Pos())
	return cm.Pos().IsValid() && hash < r.from && !strings.ContainsAny(r.src[hash:r.end], "\n") && !r.closedBetween(substs, hash)
}

// closedBetween reports whether one of substs that holds the # at hash
// closes before the insertion.
func (r *readLine) closedBetween(substs []*syntax.CmdSubst, hash int) bool {
	for _, cs := range substs {
		if end := cs.End(); end.IsValid() && !end.IsRecovered() && offset(r.src, cs.Pos()) < hash && hash < int(end.Offset()) && int(end.Offset()) <= r.from {
			return true
		}
	}
	return false
}

// spans reports whether word w holds all of the insertion (a word
// recovered at the end of the line holds the rest), and its size.
func (r *readLine) spans(w *syntax.Word) (size int, ok bool) {
	start, stop := w.Pos(), int(w.End().Offset())
	if w.End().IsRecovered() {
		stop = math.MaxInt
	} else if !w.End().IsValid() || !start.IsValid() {
		return 0, false
	}
	return stop - offset(r.src, start), offset(r.src, start) <= r.from && stop >= r.end
}

// holdsForeign reports whether a node of w other than quoting reaches into
// src[from:end]: an expansion or substitution there, or one the text
// before it opened ($ typed before the value makes $value a parameter).
func holdsForeign(w *syntax.Word, src string, from, end int) bool {
	foreign := false
	syntax.Walk(w, func(n syntax.Node) bool {
		switch n.(type) {
		case nil:
			return false
		case *syntax.Word, *syntax.Lit, *syntax.SglQuoted, *syntax.DblQuoted:
			return true
		}
		stop := math.MaxInt // a recovered end runs on
		if !n.End().IsRecovered() {
			stop = int(n.End().Offset())
		}
		foreign = foreign || offset(src, n.Pos()) < end && stop > from
		return true
	})
	return foreign
}

// offset is pos's offset in src. Inside backticks mvdan places a token
// that starts with an escape (\\, \` or \$) after the backslash the
// backticks take off; no token starts after an odd run of backslashes
// otherwise (one ends there only at a trailing backslash), so the offset
// goes back over it.
func offset(src string, pos syntax.Pos) int {
	o := int(pos.Offset())
	run := 0
	for o-run > 0 && o <= len(src) && src[o-run-1] == '\\' {
		run++
	}
	if run%2 == 1 {
		return o - run
	}
	return o
}

// at maps pos in r's line for comparison with another reading.
func (r *readLine) at(pos syntax.Pos) mappedPos {
	switch {
	case pos.IsRecovered():
		return mappedPos{zone: zoneRecovered}
	case !pos.IsValid():
		return mappedPos{zone: zoneInvalid}
	}
	switch o := offset(r.src, pos); {
	case o < r.from:
		return mappedPos{zoneBefore, o}
	case o >= r.end:
		return mappedPos{zoneAfter, o - r.end}
	default:
		return mappedPos{zoneInside, o - r.from}
	}
}

// sameNodes reports whether nodes xs of line a are ys of line b: node for
// node, the same kind with the same own fields (operators, flags, literal
// text and token positions, mapped; children are nodes of their own). With
// anonymous, a literal inside the insertion is not compared: a variable
// reference names a variable there.
func sameNodes(a *readLine, xs []syntax.Node, b *readLine, ys []syntax.Node, anonymous bool) bool {
	if len(xs) != len(ys) {
		return false
	}
	for i, x := range xs {
		if !sameNode(a, x, b, ys[i], anonymous) {
			return false
		}
	}
	return true
}

// wordNodes are the nodes of word w, in walk order.
func wordNodes(w *syntax.Word) []syntax.Node {
	var nodes []syntax.Node
	if w != nil {
		syntax.Walk(w, func(n syntax.Node) bool {
			if n != nil {
				nodes = append(nodes, n)
			}
			return n != nil
		})
	}
	return nodes
}

// expansion is what the insertion's word expands to, in its role: not
// expanded in a comment.
func (r *readLine) expansion() expansion {
	if r.word == nil {
		return expansion{}
	}
	return expandWord(r.word, r.target.role)
}

// foreign reports whether a node of the insertion's word other than
// quoting reaches into the insertion: the value would be (part of) an
// expansion.
func (r *readLine) foreign() bool {
	return r.word != nil && holdsForeign(r.word, r.src, r.from, r.end)
}

var posType = reflect.TypeFor[syntax.Pos]()

func sameNode(a *readLine, x syntax.Node, b *readLine, y syntax.Node, anonymous bool) bool {
	vx, vy := reflect.ValueOf(x).Elem(), reflect.ValueOf(y).Elem()
	if vx.Type() != vy.Type() {
		return false
	}
	inside := a.at(x.Pos()).zone == zoneInside
	for i := range vx.NumField() {
		fx, fy := vx.Field(i), vy.Field(i)
		same := true
		switch {
		case fx.Type() == posType:
			same = a.at(*fx.Addr().Interface().(*syntax.Pos)) == b.at(*fy.Addr().Interface().(*syntax.Pos))
		case fx.Kind() == reflect.String:
			same = anonymous && inside || fx.String() == fy.String()
		case fx.Kind() == reflect.Bool:
			same = fx.Bool() == fy.Bool()
		case fx.CanInt():
			same = fx.Int() == fy.Int()
		case fx.CanUint():
			same = fx.Uint() == fy.Uint()
		}
		if !same {
			return false
		}
	}
	return true
}

// wordRoleMap is the role of each word that is not an argument.
type wordRoleMap map[*syntax.Word]role

func (roles wordRoleMap) set(w *syntax.Word, r role) {
	if w != nil {
		roles[w] = r
	}
}

// of is the role of word w.
func (roles wordRoleMap) of(w *syntax.Word) role {
	if r, ok := roles[w]; ok {
		return r
	}
	return roleFields
}

// visit sets the roles of the words node n holds directly.
func (roles wordRoleMap) visit(n syntax.Node) {
	switch n := n.(type) {
	case *syntax.ArithmExp:
		roles.arith(n.X)
	case *syntax.ArithmCmd:
		roles.arith(n.X)
	case *syntax.LetClause:
		for _, x := range n.Exprs {
			roles.arith(x)
		}
	case *syntax.CStyleLoop:
		roles.arith(n.Init)
		roles.arith(n.Cond)
		roles.arith(n.Post)
	case *syntax.Assign:
		roles.arith(n.Index)
		if !n.Naked {
			roles.set(n.Value, roleLiteral)
		}
	case *syntax.Redirect:
		roles.set(n.Word, roleLiteral)
		roles.set(n.Hdoc, roleDocument)
		if n.Word != nil && n.Word.Lit() == "" { // a quoted delimiter
			roles.set(n.Hdoc, roleRaw)
		}
	case *syntax.ParamExp:
		roles.paramExp(n)
	default:
		roles.test(n)
	}
}

// test sets the roles of a case's or a [[ ]] test's words: what is matched
// is a value, what it is matched against a pattern.
func (roles wordRoleMap) test(n syntax.Node) {
	switch n := n.(type) {
	case *syntax.CaseClause:
		roles.set(n.Word, roleLiteral)
	case *syntax.CaseItem:
		for _, w := range n.Patterns {
			roles.set(w, rolePattern)
		}
	case *syntax.BinaryTest:
		x, _ := n.X.(*syntax.Word)
		y, _ := n.Y.(*syntax.Word)
		roles.set(x, roleLiteral)
		roles.set(y, roleLiteral)
		if n.Op == syntax.TsMatch || n.Op == syntax.TsNoMatch || n.Op == syntax.TsMatchShort {
			roles.set(y, rolePattern)
		}
	case *syntax.UnaryTest:
		x, _ := n.X.(*syntax.Word)
		roles.set(x, roleLiteral)
	case *syntax.TestClause: // a word on its own: is it empty
		x, _ := n.X.(*syntax.Word)
		roles.set(x, roleLiteral)
	case *syntax.ParenTest:
		x, _ := n.X.(*syntax.Word)
		roles.set(x, roleLiteral)
	}
}

// paramExp sets the roles of a ${ } expansion's words: an index or slice is
// arithmetic, a default a value, what # % ^ , and / match a pattern.
func (roles wordRoleMap) paramExp(n *syntax.ParamExp) {
	if n.Slice != nil {
		roles.arith(n.Slice.Offset)
		roles.arith(n.Slice.Length)
	}
	roles.arith(n.Index)
	if n.Exp != nil {
		roles.set(n.Exp.Word, roleLiteral)
		if n.Exp.Op >= syntax.RemSmallSuffix {
			roles.set(n.Exp.Word, rolePattern)
		}
	}
	if n.Repl != nil {
		roles.set(n.Repl.Orig, rolePattern)
		roles.set(n.Repl.With, roleLiteral)
	}
}

// arith marks the words of arithmetic expression x, but those in a
// substitution inside it, which holds commands again.
func (roles wordRoleMap) arith(x syntax.ArithmExpr) {
	if x == nil || reflect.ValueOf(x).IsNil() {
		return
	}
	syntax.Walk(x, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.Word:
			roles.set(n, roleArith)
		case nil, *syntax.CmdSubst, *syntax.ProcSubst:
			return false
		}
		return true
	})
}

// expandWord expands w the way role says, with nothing run: every variable
// holds a marker of its name, a substitution expands to a marker, and a
// glob matches nothing (and so expands to nothing). An expansion that
// fails is one field of its error, which no value reads back as; so is one
// that panics (mvdan's expand does on a pattern that is not UTF-8, as in
// ${0#\x80}): completion must not end the user's shell.
func expandWord(w *syntax.Word, r role) (words expansion) {
	defer func() {
		if p := recover(); p != nil {
			words = expansionError(fmt.Sprintf("expansion failed: %v", p))
		}
	}()
	cfg := &expand.Config{
		Env: markerEnv{},
		CmdSubst: func(out io.Writer, _ *syntax.CmdSubst) error {
			_, err := io.WriteString(out, "\x03")
			return err
		},
		ProcSubst: func(*syntax.ProcSubst) (string, error) { return "\x04", nil },
		ReadDir2:  func(string) ([]fs.DirEntry, error) { return nil, nil },
		NullGlob:  true,
	}
	var fields []string
	var s string
	var err error
	switch r {
	case roleFields:
		fields, err = expand.Fields(cfg, w)
		return expansionOf(fields, err)
	case roleLiteral:
		s, err = expand.Literal(cfg, w)
	case roleDocument:
		s, err = expand.Document(cfg, w)
	case rolePattern:
		s, err = expand.Pattern(cfg, w)
	case roleRaw:
		var b strings.Builder
		for _, part := range w.Parts {
			lit, ok := part.(*syntax.Lit)
			if !ok {
				return expansionError("not raw text")
			}
			b.WriteString(lit.Value)
		}
		s = b.String()
	default: // arithmetic: no writer writes an operand
		return expansionError("an arithmetic operand")
	}
	return expansionOf([]string{s}, err)
}

func expansionOf(fields []string, err error) expansion {
	if err != nil {
		return expansionError(err.Error())
	}
	return expandedTo(fields...)
}

// markerEnv sets every variable (and every ~name's home) to a marker of its
// name, but IFS, which keeps its default.
type markerEnv struct{}

func (markerEnv) Get(name string) expand.Variable {
	if name == "IFS" {
		return expand.Variable{}
	}
	return expand.Variable{Set: true, Kind: expand.String, Str: "\x01" + name + "\x02"}
}

func (markerEnv) Each(func(string, expand.Variable) bool) {}
