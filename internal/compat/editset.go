// internal/compat/editset.go
package compat

import (
	"fmt"
	"slices"
)

// edit replaces the source bytes [start, end) with text.
type edit struct {
	kind       editKind
	start, end int
	text       string // ":" for a skip, "" (or ";") for a drop
	reason     string // why, for a skip or a drop
	seq        int    // when the edit set made it, for undo
	// ret is the text that returns where the source shell may, for a skip
	// or drop of code that holds a return: return for a skip, else return
	// for dropped elifs, *) return;; for dropped case arms.
	ret       string
	returning returnState
}

// returnState is whether an edit returns where the code it takes away may
// (treeFilter.returnsIn).
type returnState int

const (
	// cannotReturn: an insert, or a skip or drop with no text that returns.
	cannotReturn returnState = iota
	// mayReturn: a skip or drop whose ret returns there, not in its place.
	mayReturn
	// returnsHere: ret is in its place (returnHere).
	returnsHere
)

// newSkipOrDrop is a skip or drop (kind) of the source bytes [start, end)
// with text, for reason, that may return there with ret ("": it cannot).
func newSkipOrDrop(kind editKind, start, end int, text, ret, reason string) edit {
	e := edit{kind: kind, start: start, end: end, text: text, ret: ret, reason: reason}
	if ret != "" {
		e.returning = mayReturn
	}
	return e
}

// returnHere makes e, which may return, return where the code it takes
// away may.
func (e *edit) returnHere() {
	e.text, e.returning = e.ret, returnsHere
}

// editKind is what an edit does to the source.
type editKind int

const (
	// editSkip replaces a statement with ":"; its source stays visible as
	// comments, and the report lists it.
	editSkip editKind = iota
	// editDrop removes branches (an elif to its fi, case arms to their
	// esac); the report lists it.
	editDrop
	// editInsert adds what bash needs and zsh does not (a ; before }, a :
	// in an empty body), or puts bash's spelling for zsh's (& for &!, >
	// for >!, ;;& for ;|, export PATH=... for path=(...), command -v x for
	// (( $+commands[x] ))).
	editInsert
)

// reported reports whether e takes away code the user wrote.
func (e edit) reported() bool { return e.kind != editInsert }

// overlaps reports whether e and o change some of the same source: an
// insert at the edge of another edit does not.
func (e edit) overlaps(o edit) bool {
	switch {
	case e.start == e.end:
		return o.start < e.start && e.start < o.end
	case o.start == o.end:
		return e.start < o.start && o.start < e.end
	}
	return e.start < o.end && o.start < e.end
}

// imported is a kept simple command: where it is, and its text.
type imported struct {
	at   int
	text string
	seq  int
}

// funcDef is a function definition: what a call to name is.
type funcDef struct {
	at    int
	name  string
	call  outcome // kept for a function whose status no skip decides
	exits bool    // a skip in it could exit: a call may end the shell
	seq   int
}

// editSet is what a walk found in the source: the edits, the kept simple
// commands (for the report) and the function definitions, in the order the
// walk made them. Two ways take them back: undo, of all made since a mark
// (a statement skipped whole after its parts were walked), and cut, of all
// in a range of the source (the statements a stop comments out). add keeps
// the edits disjoint, which render relies on: it refuses one that overlaps
// another, and the walk then fails, unless undo takes the refusal back.
type editSet struct {
	edits   []edit
	imports []imported
	funcs   []funcDef
	made    int     // how many entries the set made: what a mark holds
	refused refusal // the first edit add or cut refused, if any
	// reach is at or past the end of every edit: one that starts there
	// overlaps none, which spares add the check for most edits, made in
	// source order.
	reach int
}

// mark is how far a walk got, to undo what it made since.
type mark int

// refusal is why the set refused an edit, and when (an entry's sequence
// number): undo takes it back with the statement it was made in.
type refusal struct {
	err error
	at  int // the source offset of the edit refused
	seq int
}

// refuse records err, why the edit at source offset at was refused, unless
// one was refused already.
func (s *editSet) refuse(at int, err error) {
	if s.refused.err == nil {
		s.refused = refusal{err: err, at: at, seq: s.next()}
	}
}

// clear empties s for a new walk.
func (s *editSet) clear() {
	s.edits, s.imports, s.funcs, s.refused, s.reach = s.edits[:0], s.imports[:0], s.funcs[:0], refusal{}, 0
}

func (s *editSet) next() int {
	s.made++
	return s.made
}

// add adds edit e, unless it overlaps one already made.
func (s *editSet) add(e edit) {
	if e.start < s.reach {
		if i := slices.IndexFunc(s.edits, e.overlaps); i >= 0 {
			o := s.edits[i]
			s.refuse(min(e.start, o.start), fmt.Errorf("an edit of bytes %d-%d overlaps one of %d-%d", e.start, e.end, o.start, o.end))
			return
		}
	}
	e.seq = s.next()
	s.edits = append(s.edits, e)
	s.reach = max(s.reach, e.end)
}

// keep records the kept simple command text at source offset at.
func (s *editSet) keep(at int, text string) {
	s.imports = append(s.imports, imported{at: at, text: text, seq: s.next()})
}

// define records d, a definition of a function, and what a call to it is
// from there on.
func (s *editSet) define(d funcDef) {
	d.seq = s.next()
	s.funcs = append(s.funcs, d)
}

// def returns what a call at source offset at to the command name is: the
// last definition of it before at, or the zero one, a kept call that ends
// nothing, for any other command.
func (s *editSet) def(name string, at int) funcDef {
	for i := len(s.funcs) - 1; i >= 0; i-- {
		if d := s.funcs[i]; d.name == name && d.at < at {
			return d
		}
	}
	return funcDef{}
}

func (s *editSet) mark() mark { return mark(s.made) }

// undo takes back everything made since m, a refusal too (an edit refused
// since m was one of the statement now taken back).
func (s *editSet) undo(m mark) {
	after := int(m)
	s.edits = slices.DeleteFunc(s.edits, func(e edit) bool { return e.seq > after })
	s.imports = slices.DeleteFunc(s.imports, func(im imported) bool { return im.seq > after })
	s.funcs = slices.DeleteFunc(s.funcs, func(f funcDef) bool { return f.seq > after })
	if s.refused.seq > after {
		s.refused = refusal{}
	}
}

// since returns the edits made since m.
func (s *editSet) since(m mark) []edit {
	return slices.DeleteFunc(slices.Clone(s.edits), func(e edit) bool { return e.seq <= int(m) })
}

// cut adds e, a skip or drop, taking back everything made in the source it
// replaces.
func (s *editSet) cut(e edit) {
	start, end := e.start, e.end
	in := func(o int) bool { return start <= o && o < end }
	if i := slices.IndexFunc(s.edits, func(e edit) bool { return in(e.start) && e.end > end }); i >= 0 {
		s.refuse(start, fmt.Errorf("a cut of bytes %d-%d ends inside an edit of %d-%d", start, end, s.edits[i].start, s.edits[i].end))
	}
	s.edits = slices.DeleteFunc(s.edits, func(e edit) bool { return in(e.start) })
	s.imports = slices.DeleteFunc(s.imports, func(im imported) bool { return in(im.at) })
	s.funcs = slices.DeleteFunc(s.funcs, func(f funcDef) bool { return in(f.at) })
	s.add(e)
}

// removed reports whether source offset o is in a skip or drop of edits.
func removed(edits []edit, o int) bool {
	return slices.ContainsFunc(edits, func(e edit) bool { return e.reported() && e.start <= o && o < e.end })
}

// removedIn reports whether source offset o is in a skip or drop within the
// source bytes [start, end].
func (s *editSet) removedIn(o, start, end int) bool { return s.holding(o, start, end) != nil }

// holding returns the skip or drop within the source bytes [start, end]
// that source offset o is in, or nil.
func (s *editSet) holding(o, start, end int) *edit {
	i := slices.IndexFunc(s.edits, func(e edit) bool {
		return e.reported() && start <= e.start && e.end <= end && e.start <= o && o < e.end
	})
	if i < 0 {
		return nil
	}
	return &s.edits[i]
}

// replaced reports whether a skip or drop replaced all of the source bytes
// [start, end).
func (s *editSet) replaced(start, end int) bool {
	return slices.ContainsFunc(s.edits, func(e edit) bool { return e.reported() && e.start <= start && end <= e.end })
}

// sorted returns the edits in source order, an insert before an edit that
// starts where it is.
func (s *editSet) sorted() []edit {
	slices.SortStableFunc(s.edits, func(a, b edit) int {
		if a.start != b.start {
			return a.start - b.start
		}
		return a.end - b.end
	})
	return s.edits
}
