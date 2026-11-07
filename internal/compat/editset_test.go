package compat

import (
	"strings"
	"testing"
)

// The edit set's transitions: add refuses an edit that overlaps another
// (an insert at an edit's edge does not), undo takes back what was made
// since a mark whatever a cut took out before it, and cut takes back what
// is in its range, function definitions too, and refuses to end inside an
// edit.
func TestEditSet_Transitions(t *testing.T) {
	var s editSet
	s.add(edit{kind: editSkip, start: 10, end: 20, text: ":"})
	s.add(edit{kind: editInsert, start: 20, end: 20, text: ";"}) // at the edge
	s.add(edit{kind: editInsert, start: 10, end: 10, text: ";"})
	if s.refused.err != nil || len(s.edits) != 3 {
		t.Fatalf("edge inserts refused: %v %+v", s.refused.err, s.edits)
	}
	s.add(edit{kind: editInsert, start: 15, end: 15, text: ";"})
	if s.refused.err == nil || len(s.edits) != 3 {
		t.Fatalf("an insert inside a skip was taken: %+v", s.edits)
	}
	s.clear()
	if s.refused.err != nil || len(s.edits) != 0 {
		t.Fatalf("clear left %v %+v", s.refused.err, s.edits)
	}
	s.add(edit{kind: editSkip, start: 5, end: 25, text: ":"})
	s.add(edit{kind: editDrop, start: 0, end: 6})
	if s.refused.err == nil || !strings.Contains(s.refused.err.Error(), "overlaps") {
		t.Fatalf("overlapping drop: err %v", s.refused.err)
	}

	s.clear()
	s.define(funcDef{at: 0, name: "f", call: outcome{status: stmtUnknown, reason: "r"}})
	m := s.mark()
	s.keep(30, "alias a=b")
	s.define(funcDef{at: 30, name: "f"}) // a kept redefinition
	s.add(edit{kind: editSkip, start: 40, end: 50, text: ":"})
	if s.def("f", 100).call.status != stmtKept || s.def("g", 100).call.status != stmtKept {
		t.Fatalf("call after a kept redefinition: %+v", s.def("f", 100).call)
	}
	if got := len(s.since(m)); got != 1 {
		t.Fatalf("since(m) = %d edits, want 1", got)
	}
	s.cut(edit{kind: editSkip, start: 35, end: 60, text: ":", reason: "stopped"})
	if len(s.edits) != 1 || s.edits[0].reason != "stopped" || len(s.imports) != 1 {
		t.Fatalf("cut: edits %+v imports %+v", s.edits, s.imports)
	}
	s.undo(m)
	if len(s.edits) != 0 || len(s.imports) != 0 {
		t.Fatalf("undo left edits %+v imports %+v", s.edits, s.imports)
	}
	if o := s.def("f", 100).call; o.status != stmtUnknown {
		t.Fatalf("undo kept the redefinition: %+v", o)
	}
	s.define(funcDef{at: 70, name: "g", call: outcome{status: stmtUnknown}})
	s.cut(edit{kind: editSkip, start: 65, end: 80, text: ":", reason: "stopped"})
	if s.def("g", 100).call.status != stmtKept {
		t.Fatalf("cut kept a definition in its range")
	}

	s.clear()
	s.add(edit{kind: editSkip, start: 10, end: 30, text: ":"})
	s.cut(edit{kind: editSkip, start: 5, end: 20, text: ":", reason: "stopped"})
	if s.refused.err == nil || !strings.Contains(s.refused.err.Error(), "ends inside") {
		t.Fatalf("cut ending inside an edit: err %v", s.refused.err)
	}
}

// A refused edit is part of what undo takes back: made inside a statement
// later skipped whole, it is gone with that statement's edits, and the file
// stays with the tree filter. One made before the mark stays.
func TestEditSet_UndoTakesBackARefusal(t *testing.T) {
	var s editSet
	m := s.mark()
	s.add(edit{kind: editSkip, start: 10, end: 20, text: ":"})
	s.add(edit{kind: editInsert, start: 15, end: 15, text: ";"}) // refused
	s.undo(m)
	s.add(edit{kind: editSkip, start: 5, end: 25, text: ":"}) // the statement, whole
	if s.refused.err != nil {
		t.Fatalf("undo kept the refusal: %v", s.refused.err)
	}

	s.clear()
	s.add(edit{kind: editSkip, start: 10, end: 30, text: ":"})
	s.cut(edit{kind: editSkip, start: 5, end: 20, text: ":"}) // refused: ends inside
	m = s.mark()
	s.add(edit{kind: editSkip, start: 40, end: 50, text: ":"})
	s.undo(m)
	if s.refused.err == nil {
		t.Fatal("undo took back a refusal made before its mark")
	}
	s.undo(0)
	if s.refused.err != nil {
		t.Fatalf("undo to the start kept the refusal: %v", s.refused.err)
	}
}

// A skip or drop with a text that returns may return where the code it
// takes away may, once: returnHere puts that text in its place. One with
// none, and an insert, cannot.
func TestEdit_ReturnStates(t *testing.T) {
	skip := newSkipOrDrop(editSkip, 0, 5, ":", "return", "r")
	if skip.returning != mayReturn {
		t.Fatalf("a skip with a return text: %v, want mayReturn", skip.returning)
	}
	skip.returnHere()
	if skip.returning != returnsHere || skip.text != "return" {
		t.Errorf("after returnHere: %v %q, want returnsHere and its return text", skip.returning, skip.text)
	}
	if drop := newSkipOrDrop(editDrop, 0, 5, ";", "", "r"); drop.returning != cannotReturn {
		t.Errorf("a drop with no return text: %v, want cannotReturn", drop.returning)
	}
	if ins := (edit{kind: editInsert, text: ";"}); ins.returning != cannotReturn {
		t.Errorf("an insert: %v, want cannotReturn", ins.returning)
	}
}
