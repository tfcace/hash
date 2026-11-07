package history

import (
	"strings"
	"testing"
)

// Typing into a search with no rows yet: until the debounced search for the
// new query lands, the view must not claim there is nothing ("No matches"),
// only that it is searching.
func TestSearchUI_UnsearchedQuerySaysSearching(t *testing.T) {
	ui := newStateSearchUI(t, "git status")
	ui.Init()
	typeKey(ui, "z") // no match: rows empty
	ui.fresh()       // ...and searched
	if s := ui.View().Content; !strings.Contains(s, "No matches") {
		t.Fatalf("a searched query with no rows should say No matches:\n%s", s)
	}
	typeKey(ui, "q") // not searched yet
	s := ui.View().Content
	if strings.Contains(s, "No matches") || strings.Contains(s, "No history") {
		t.Errorf("an unsearched query claims there is nothing:\n%s", s)
	}
	if !strings.Contains(s, "searching") {
		t.Errorf("an unsearched query should say it is searching:\n%s", s)
	}
}

// The query is typed text: drawn inert in the header.
func TestSearchUI_QueryDrawnInert(t *testing.T) {
	ui := newStateSearchUI(t, "git status")
	ui.query = "a\x1b[2Jb"
	if s := ui.View().Content; strings.Contains(s, "\x1b[2J") {
		t.Errorf("query drawn raw: %q", s)
	}
}
