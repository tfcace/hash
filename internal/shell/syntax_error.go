package shell

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"mvdan.cc/sh/v3/syntax"

	"github.com/tfcace/hash/internal/executor"
	"github.com/tfcace/hash/internal/termtext"
)

// HandleSyntaxError draws a syntax error in source as a banner that shows
// the line and points at the column, and reports whether err was one.
//
//	┌ ✗ syntax error · line 1
//	│ echo $(( 1 + ); echo hi
//	│           ┬
//	│           ╰ `+` must be followed by an expression
//	└─ ↑ to edit · ?? to explain
func (h *ErrorHandler) HandleSyntaxError(source string, err error) bool {
	pos, ok := syntaxErrorPos(err)
	if !ok {
		return false
	}
	_, msg := executor.SyntaxError(err)
	out, red := h.writer(), fgCode(h.colors().Error)
	fmt.Fprintf(out, "\n%s\n", frameTop("✗ syntax error", fmt.Sprintf("line %d", max(pos.Line(), 1)), red))

	lines := strings.Split(source, "\n")
	if pos.Line() < 1 || int(pos.Line()) > len(lines) {
		// No line to point into: the message alone.
		fmt.Fprintln(out, frameRow(red+msg+ansiReset))
		fmt.Fprintln(out, frameFoot("↑ to edit", "?? to explain"))
		return true
	}
	line := lines[pos.Line()-1]
	at := min(max(int(pos.Col())-1, 0), len(line))
	for at > 0 && at < len(line) && !utf8.RuneStart(line[at]) {
		at--
	}
	before, after := termtext.Inert(line[:at]), termtext.Inert(line[at:])

	// No row may wrap. A line or message too long for the pane is cut
	// around the pointer, which moves to the pane's first third.
	if budget := rowBudget(terminalColumns()) - 2; budget > 12 {
		need := runewidth.StringWidth(before) + max(runewidth.StringWidth(after), 2+runewidth.StringWidth(msg))
		if need > budget && runewidth.StringWidth(before) > budget/3 {
			before = "…" + lastColumns(before, budget/3-1)
		}
		room := budget - runewidth.StringWidth(before)
		after, msg = fitText(after, room), fitText(msg, room-2)
	}

	indent := strings.Repeat(" ", runewidth.StringWidth(before))
	fmt.Fprintln(out, frameRow(before+after))
	fmt.Fprintln(out, frameRow(indent+red+"┬"+ansiReset))
	fmt.Fprintln(out, frameRow(indent+red+"╰ "+msg+ansiReset))
	fmt.Fprintln(out, frameFoot("↑ to edit", "?? to explain"))
	return true
}

// syntaxErrorPos is where a parse error points, and whether err is one.
func syntaxErrorPos(err error) (syntax.Pos, bool) {
	var parseErr syntax.ParseError
	if errors.As(err, &parseErr) {
		return parseErr.Pos, true
	}
	var langErr syntax.LangError
	if errors.As(err, &langErr) {
		return langErr.Pos, true
	}
	return syntax.Pos{}, false
}

// lastColumns is the end of s, at most width columns of it.
func lastColumns(s string, width int) string {
	runes := []rune(s)
	start := len(runes)
	for used := 0; start > 0; start-- {
		used += runewidth.RuneWidth(runes[start-1])
		if used > width {
			break
		}
	}
	return string(runes[start:])
}
