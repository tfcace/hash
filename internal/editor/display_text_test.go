package editor

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/termtext"
)

// renderGhost renders line (cursor at its end) with ghost text and returns
// the bytes written.
func renderGhost(line, ghost string, state GhostRenderState) string {
	var out bytes.Buffer
	d := NewDisplay(&out, 200, 24)
	buf := NewBufferFromString(line)
	cur := NewCursor()
	cur.MoveTo(0, len(line))
	d.RenderWithGhost(buf, cur, false, ghost, state)
	return out.String()
}

// Agent text is untrusted: an ESC in it must not reach the terminal as a
// control sequence (here one that clears the screen), and the other control
// characters show visibly instead of moving the cursor.
func TestDisplay_GhostControlCharactersShowVisibly(t *testing.T) {
	for _, state := range []GhostRenderState{
		{FromAgent: true},
		{FromAgent: true, Streaming: true},
		{},
	} {
		out := renderGhost("echo ", "hi\x1b[2J\x07\rthere", state)
		if strings.Contains(out, "\x1b[2J") || strings.Contains(out, "\x07") || strings.Contains(out, "\rthere") {
			t.Errorf("state %+v: control characters reached the terminal raw: %q", state, out)
		}
		if !strings.Contains(out, "hi^[[2J^G^Mthere") {
			t.Errorf("state %+v: want caret notation in %q", state, out)
		}
	}
}

// The agent's status and failure notice are untrusted too.
func TestDisplay_GhostStatusAndNoticeAreSanitized(t *testing.T) {
	for _, state := range []GhostRenderState{
		{FromAgent: true, Streaming: true, Status: "run\x1b]0;pwned\x07ning"},
		{Notice: "agent: \x1b[31mred"},
	} {
		out := renderGhost("x", "", state)
		if strings.Contains(out, "\x1b]0;") || strings.Contains(out, "\x1b[31m") {
			t.Errorf("state %+v: escape reached the terminal raw: %q", state, out)
		}
	}
}

var cursorForward = regexp.MustCompile(`\r\x1b\[(\d+)C\x1b\[\?25h$`)

// cursorCol is the column the render leaves the cursor at (after its \r).
func cursorCol(t *testing.T, out string) int {
	t.Helper()
	m := cursorForward.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no final cursor position in %q", out)
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// A tab in the buffer (pasted) is drawn as the same number of cells the
// cursor math counts. It counted zero cells while the terminal moved to the
// next tab stop, so the cursor drifted left of the text.
func TestDisplay_TabWidthMatchesCursor(t *testing.T) {
	plain := cursorCol(t, renderGhost("a"+strings.Repeat(" ", termtext.TabWidth)+"b", "", GhostRenderState{}))
	tabbed := renderGhost("a\tb", "", GhostRenderState{})
	if strings.Contains(tabbed, "\t") {
		t.Errorf("raw tab written: %q", tabbed)
	}
	if got := cursorCol(t, tabbed); got != plain {
		t.Errorf("cursor col after a tab = %d, want %d (as if the tab were %d spaces)", got, plain, termtext.TabWidth)
	}
}

// Ghost text width matches what is drawn, so a wrapped line keeps the
// cursor on its row: a ghost with a tab counted zero for it.
func TestDisplay_GhostWidthCountsSanitizedText(t *testing.T) {
	if got, want := termtext.Width("a\tb\x1b"), 2+termtext.TabWidth+2; got != want {
		t.Errorf("textWidth = %d, want %d", got, want)
	}
}

// Completion items come from the filesystem and other tools: a filename
// with an ESC in it must not drive the terminal.
func TestDisplay_CompletionItemsAreSanitized(t *testing.T) {
	var out bytes.Buffer
	d := NewDisplay(&out, 80, 24)
	d.RenderCompletionMenu([]CompletionItem{{Text: "evil\x1b[2J", Description: "d\x1b[31m"}, {Text: "ok"}}, 0, 0, 0, "")
	if strings.Contains(out.String(), "\x1b[2J") || strings.Contains(out.String(), "\x1b[31m") {
		t.Errorf("escape in a completion item reached the terminal raw: %q", out.String())
	}
}

// The agent's status line is laid out at its own width: the layout counted
// " Agent thinking..." whatever the status said, so a long status that
// wrapped left the cursor a row off.
func TestDisplay_StatusWidthIsItsOwn(t *testing.T) {
	var out bytes.Buffer
	d := NewDisplay(&out, 25, 24)
	buf := NewBufferFromString("ab")
	cur := NewCursor()
	cur.MoveTo(0, 2)
	d.RenderWithGhost(buf, cur, false, "", GhostRenderState{FromAgent: true, Streaming: true, Status: strings.Repeat("s", 30)})
	if !strings.Contains(out.String(), "\x1b[1A\r") {
		t.Errorf("a status that wraps should leave the cursor one row up from the end: %q", out.String())
	}
}
