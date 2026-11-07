package editor

import (
	"fmt"
	"strings"
)

// CompletionHidden is what a menu leaves out of the matches: its items
// are not all of them, so a lone item is not the only match.
type CompletionHidden struct {
	Withheld int // matches that could not be inserted safely there
	// More are matches past the menu's limit, or not checked in time, as
	// far as they were counted; Uncounted, that there were more than More.
	More      int
	Uncounted bool
}

// any reports whether the menu left anything out.
func (h CompletionHidden) any() bool { return h.Withheld > 0 || h.More > 0 || h.Uncounted }

// note is what the menu says under its items about what it left out, and
// what to do about matches not shown, or "" for nothing.
func (h CompletionHidden) note() string {
	var parts []string
	switch {
	case h.Withheld == 1:
		parts = append(parts, "1 match can't be typed safely here")
	case h.Withheld > 1:
		parts = append(parts, fmt.Sprintf("%d matches can't be typed safely here", h.Withheld))
	}
	switch {
	case h.Uncounted:
		parts = append(parts, "more not shown, type more to narrow")
	case h.More > 0:
		parts = append(parts, fmt.Sprintf("%d more not shown, type more to narrow", h.More))
	}
	return strings.Join(parts, "; ")
}
