package editor

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var forwardRe = regexp.MustCompile(`\x1b\[(\d+)C`)

// lastForward is the last cursor-forward distance in out (0 for none).
func lastForward(out string) int {
	m := forwardRe.FindAllStringSubmatch(out, -1)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[len(m)-1][1])
	return n
}

// menuEditor opens a two-item menu on line, with the editor's output in out.
func menuEditor(t *testing.T, line string, out *bytes.Buffer) *Editor {
	t.Helper()
	ed := New(Config{CompleteOutcomeFunc: func(line string, pos int) CompletionOutcome {
		return wordMenu(line, pos, Completion{Text: "alpha"}, Completion{Text: "alps"})
	}}, strings.NewReader(""), out)
	ed.SetInitialText(line)
	out.Reset()
	press(t, ed, keyTab())
	return ed
}

// The menu opens under the word and leaves the cursor where the input
// render put it, in cells: columns were byte offsets, so after wide or
// multibyte text the menu sat right of its word and the cursor was left
// past the end of the line while the menu was open.
func TestDisplay_MenuUsesCellColumns(t *testing.T) {
	var out bytes.Buffer
	menuEditor(t, "日本 a", &out)
	s := out.String()
	menuStart := strings.Index(s, "\r\n")
	if menuStart < 0 {
		t.Fatalf("no menu drawn: %q", s)
	}
	input, menu := s[:menuStart], s[menuStart:]
	// prompt " " (1) + "日本 " (5 cells): the word starts at cell 6.
	if !strings.Contains(menu, "\x1b[2K\x1b[6C") {
		t.Errorf("menu rows should start at cell 6 under the word: %q", menu)
	}
	if got, want := lastForward(menu), lastForward(input); got != want {
		t.Errorf("menu left the cursor at cell %d, the input render put it at %d", got, want)
	}
}

// Closing the menu is a redraw: the clear below the input takes the menu
// rows, and no newline is written (near the terminal's bottom one would
// scroll the prompt away from the cursor).
func TestEditor_EscapeClearsMenuWithoutNewlines(t *testing.T) {
	var out bytes.Buffer
	ed := menuEditor(t, "ls a", &out)
	out.Reset()
	press(t, ed, Key{Special: KeyEscape})
	s := out.String()
	if ed.menu != nil || !strings.Contains(s, ansiClearToEnd) {
		t.Fatalf("menu still open or not cleared: %q", s)
	}
	if strings.Contains(s, "\n") {
		t.Errorf("closing the menu wrote a newline: %q", s)
	}
}
