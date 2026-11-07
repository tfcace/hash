package editor

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// A multi-line input with the cursor on its first line: the menu and the
// notice go below the input's last row. They used to start on the row under
// the cursor and overwrite the lines below it.
func TestDisplay_MenuAndNoticeGoBelowTheInput(t *testing.T) {
	draw := func(t *testing.T, extra func(d *Display)) string {
		t.Helper()
		var out bytes.Buffer
		d := NewDisplay(&out, 80, 24)
		buf := NewBufferFromString("ls a\nsecond line\nthird")
		cur := NewCursor()
		cur.MoveTo(0, 4)
		d.Render(buf, cur, false)
		out.Reset()
		extra(d)
		return out.String()
	}
	items := []CompletionItem{{Text: "alpha"}, {Text: "alps"}}
	cases := map[string]struct {
		draw func(d *Display)
		rows int
	}{
		"menu":   {func(d *Display) { d.RenderCompletionMenu(items, 0, 0, 3, "") }, len(items)},
		"notice": {func(d *Display) { d.RenderCompletionNotice("no matches") }, 1},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := draw(t, c.draw)
			down := fmt.Sprintf(ansiCursorDown, 2)
			if !strings.HasPrefix(s, down) {
				t.Errorf("does not first move past the 2 input rows below the cursor: %q", s)
			}
			if up := fmt.Sprintf(ansiCursorUp, c.rows+2); !strings.Contains(s, up) {
				t.Errorf("does not return over its %d rows and the 2 input rows (%q): %q", c.rows, up, s)
			}
		})
	}
}
