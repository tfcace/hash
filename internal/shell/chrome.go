package shell

import (
	"os"
	"regexp"
	"strings"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

// terminalColumns is the pane width hash fits its own rows to (spinner,
// prompts, hints, banners), 0 when stdout is not a terminal. A row as wide
// as the pane wraps, and hash erases these rows by count, so a wrapped one
// leaves a piece behind. A variable so tests can set a width.
var terminalColumns = func() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 0
	}
	return w
}

// rowBudget is how many columns a row may use in a pane width columns wide:
// one less, so the terminal never wraps it. 0 means no limit.
func rowBudget(width int) int {
	if width <= 1 {
		return 0
	}
	return width - 1
}

// fitHint lays out a hint row, prefix + "scope · " + hints joined by " · ",
// in a pane width columns wide. It keeps the prefix, drops the scope label
// before any key, then drops whole hints from the end: a hint is never cut
// mid-word.
func fitHint(prefix, scope string, hints []string, width int) string {
	budget := rowBudget(width)
	build := func(withScope bool, n int) string {
		parts := hints[:n]
		if withScope && scope != "" {
			parts = append([]string{scope}, parts...)
		}
		return prefix + strings.Join(parts, " · ")
	}
	if budget == 0 {
		return build(true, len(hints))
	}
	if row := build(true, len(hints)); runewidth.StringWidth(row) <= budget {
		return row
	}
	for n := len(hints); n > 1; n-- {
		if row := build(false, n); runewidth.StringWidth(row) <= budget {
			return row
		}
	}
	return build(false, min(1, len(hints)))
}

// fitText truncates plain text to width columns, ending in "…" when cut.
// A width below 1 leaves it whole.
func fitText(s string, width int) string {
	if width < 1 || runewidth.StringWidth(s) <= width {
		return s
	}
	return runewidth.Truncate(s, width, "…")
}

var sgrSeqRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// visibleCols is the width of a styled string with its colors stripped.
func visibleCols(s string) int {
	return runewidth.StringWidth(sgrSeqRe.ReplaceAllString(s, ""))
}
