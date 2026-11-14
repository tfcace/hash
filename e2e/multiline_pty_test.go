//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// shiftEnter is Shift+Enter as a terminal with the kitty keyboard protocol
// sends it (hash turns the protocol on at its prompt).
const shiftEnter = "\x1b[13;2u"

// A for loop entered over three rows runs once, as one line, whether the
// rows are joined with Shift+Enter or with Enter on the unfinished text.
func TestMultiline_AForLoopOverThreeRowsRunsOnce(t *testing.T) {
	for _, tt := range []struct{ name, newline string }{
		{"shift+enter", shiftEnter},
		{"enter on unfinished text", "\r"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHome(t)
			ht := startHash(t, h, termSize{})
			ht.waitPrompts(1, startupBound)

			for _, row := range []string{"for i in 1 2 3; do", `  echo "row$i"`} {
				ht.typeText(row)
				time.Sleep(50 * time.Millisecond)
				ht.typeText(tt.newline)
				time.Sleep(50 * time.Millisecond)
			}
			ht.typeText("done\r")
			ht.waitFor("row3", 5*time.Second)
			ht.waitPrompts(2, 5*time.Second)
			ht.settle(300 * time.Millisecond)

			if got := ht.outputLines(`row\d`); strings.Join(got, ",") != "row1,row2,row3" {
				t.Errorf("the loop printed %q, want row1,row2,row3 once", got)
			}
			if n := ht.prompts(); n != 2 {
				t.Errorf("hash showed %d prompts, want 2 (the loop ran as one line)", n)
			}
			if plain := ht.plain(); strings.Contains(plain, "syntax error") || strings.Contains(plain, "not found") {
				t.Errorf("a row ran on its own:\n%s", plain)
			}
			ht.exit()
		})
	}
}

// A here-document whose delimiter is quoted reads on, as an unquoted one
// does: Enter adds body rows until the delimiter line, and the line runs
// once with its body as the command's input. Enter ran `cat <<'EOF'` at
// once, and each body line then ran as a command.
func TestMultiline_AQuotedHeredocReadsOnToItsDelimiter(t *testing.T) {
	for _, tt := range []struct{ name, op, end string }{
		{"single quotes", `<<'EOF'`, "EOF"},
		{"double quotes", `<<"EOF"`, "EOF"},
		{"backslash", `<<\EOF`, "EOF"},
		{"tab-stripping", `<<-'EOF'`, "EOF"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHome(t)
			ht := startHash(t, h, termSize{})
			ht.waitPrompts(1, startupBound)

			for _, row := range []string{"cat " + tt.op + " | tr a-z A-Z", "echo ran-$((1+1))"} {
				ht.typeText(row + "\r")
				time.Sleep(50 * time.Millisecond)
			}
			ht.typeText(tt.end + "\r")
			ht.waitPrompts(2, 5*time.Second)
			ht.settle(300 * time.Millisecond)

			if got := ht.outputLines(`ECHO RAN-\$\(\(1\+1\)\)`); len(got) != 1 {
				t.Errorf("the here-document printed %q, want its body once, read as text", got)
			}
			if plain := ht.plain(); strings.Contains(plain, "ran-2") || strings.Contains(plain, "not found") {
				t.Errorf("a body row ran as a command:\n%s", plain)
			}
			if n := ht.prompts(); n != 2 {
				t.Errorf("hash showed %d prompts, want 2 (the here-document ran as one line)", n)
			}
			ht.exit()
		})
	}
}

// Lines whose parentheses hold a # (a regex alternation, extglob patterns)
// are finished: Enter runs them. Hash's own lexer read the # as a comment,
// so Enter only added a row and the line never ran.
func TestMultiline_ALineWithAHashInParenthesesRunsOnEnter(t *testing.T) {
	h := newTestHome(t)
	ht := startHash(t, h, termSize{})
	ht.waitPrompts(1, startupBound)

	for _, line := range []string{
		`l='#x'; [[ $l =~ ^(#|//) ]] && echo m1`,
		`x='#'; [[ $x == +(#) ]] && echo m2`,
	} {
		if got := ht.command(line, 5*time.Second); !strings.Contains(got, "m") {
			t.Errorf("%s printed %q, want it to run on Enter", line, got)
		}
	}
	ht.exit()
}

// A buffer taller than the pane is drawn in a viewport of the pane's
// height: typing it, and moving the cursor through it, never pushes its
// rows into the terminal's scrollback again and again. Each redraw used to
// write the whole buffer, flooding scrollback with copies of it.
func TestMultiline_ATallBufferDoesNotFloodScrollback(t *testing.T) {
	const rows = 24
	h := newTestHome(t)
	ht := startHash(t, h, termSize{rows: rows, cols: 80})
	ht.waitPrompts(1, startupBound)

	// 40 rows: an unfinished for loop takes each Enter as a new row (the
	// last Enter adds an empty 41st, where the cursor is).
	ht.typeText("for i in 1; do\r")
	for i := 1; i < 40; i++ {
		ht.typeText(fmt.Sprintf("  echo line%02d\r", i))
		time.Sleep(15 * time.Millisecond)
	}
	ht.settle(300 * time.Millisecond)
	typed := ht.scrolled()
	t.Logf("typing the buffer scrolled %d rows", typed)
	// The buffer reaches the pane's bottom once; past that, the viewport
	// follows the cursor. Scrolling the 40 rows through once is the most a
	// terminal needs.
	if typed > 40 {
		t.Errorf("typing a 40-row buffer scrolled %d rows into scrollback, want at most 40", typed)
	}

	for range 40 {
		ht.typeText("\x1b[A")
		time.Sleep(15 * time.Millisecond)
	}
	ht.settle(300 * time.Millisecond)
	if got := ht.scrolled(); got != typed {
		t.Errorf("moving up through the buffer scrolled %d more rows into scrollback, want none", got-typed)
	}
	screen := strings.Join(ht.screenLines(), "\n")
	if !strings.Contains(screen, "for i in 1; do") {
		t.Errorf("with the cursor on the first row, the screen does not show it:\n%s", screen)
	}
	for range 40 {
		ht.typeText("\x1b[B")
		time.Sleep(15 * time.Millisecond)
	}
	ht.settle(300 * time.Millisecond)
	if got := ht.scrolled(); got != typed {
		t.Errorf("moving back down scrolled %d more rows into scrollback, want none", got-typed)
	}
	if screen := strings.Join(ht.screenLines(), "\n"); !strings.Contains(screen, "echo line39") {
		t.Errorf("with the cursor on the last row, the screen does not show it:\n%s", screen)
	}

	// Ctrl+C drops the buffer (in place: no new prompt mark).
	ht.typeText("\x03")
	ht.settle(300 * time.Millisecond)
	if screen := strings.Join(ht.screenLines(), "\n"); strings.Contains(screen, "echo line") {
		t.Errorf("Ctrl+C left the buffer on the screen:\n%s", screen)
	}
	if got := ht.command("echo cleared", 5*time.Second); got != "cleared\n" {
		t.Errorf("the line after Ctrl+C printed %q, want only cleared", got)
	}
	ht.exit()
}
