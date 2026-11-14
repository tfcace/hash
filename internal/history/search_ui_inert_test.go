package history

import (
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/prompt"
)

// History holds whatever was typed or pasted: a command with an OSC title
// sequence must not retitle the terminal when the search lists it, and a
// multi-line command is one row of the list.
func TestSearchUI_DrawsCommandsInertOnOneRow(t *testing.T) {
	ui := NewSearchUI(nil, prompt.Palette{})
	ui.width = 120
	ui.setResults(0, []Command{
		{Command: "echo hi\x1b]0;pwned\x07", Timestamp: time.Now(), Cwd: "/tmp/\x1b[31mred", GitBranch: "b\x1b[2J"},
		{Command: "for i in 1 2\ndo echo $i\ndone", Timestamp: time.Now()},
	})
	for _, selected := range []int{0, 1} {
		ui.selected = selected
		s := ui.View().Content
		for _, raw := range []string{"\x1b]0;pwned", "\x07", "\x1b[31mred", "\x1b[2J"} {
			if strings.Contains(s, raw) {
				t.Errorf("selected %d: view draws %q raw", selected, raw)
			}
		}
		rows := 0
		for _, line := range strings.Split(s, "\n") {
			if strings.Contains(line, "for i in 1 2") && strings.Contains(line, "done") {
				rows++
			}
		}
		if rows == 0 {
			t.Errorf("selected %d: the multi-line command is not on one row:\n%s", selected, s)
		}
	}
}
