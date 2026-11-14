package editor

import (
	"bytes"
	"strings"
	"testing"
)

// A notice wider than the terminal wrapped onto a second row while the
// cursor-up back to the input counted one, leaving the cursor a row low.
func TestDisplay_NoticeFitsOneRow(t *testing.T) {
	var out bytes.Buffer
	d := NewDisplay(&out, 20, 24)
	d.Render(NewBufferFromString("ls"), &Cursor{Pos: Position{0, 2}}, false)
	out.Reset()
	d.RenderCompletionNotice("paste cut short: the rest was dropped")
	row := strings.TrimPrefix(out.String(), "\r\n")
	row = row[:strings.Index(row, "\x1b[1A")]
	if w := visibleWidth(row); w >= 20 {
		t.Errorf("notice row is %d cells in a 20-cell terminal: %q", w, row)
	}
}
