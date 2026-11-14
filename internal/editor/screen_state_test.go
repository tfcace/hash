package editor

import (
	"bytes"
	"strings"
	"testing"
)

// The screen's state moves current -> stale on a change and back on the
// redraw; once the input is finalized or cleared nothing redraws it, even
// a change that lands after (a ghost update, a late completion).
func TestEditor_ScreenState(t *testing.T) {
	for _, end := range []struct {
		name string
		do   func(*Editor)
	}{
		{"finalized", (*Editor).finalizeInput},
		{"cleared", (*Editor).clearInput},
	} {
		var out bytes.Buffer
		ed := New(Config{}, strings.NewReader(""), &out)
		ed.SetInitialText("ls")
		if ed.screen != screenCurrent {
			t.Fatalf("new editor: screen %v, want current", ed.screen)
		}
		ed.changed()
		if ed.screen != screenStale {
			t.Fatalf("after a change: screen %v, want stale", ed.screen)
		}
		ed.redraw()
		if ed.screen != screenCurrent || out.Len() == 0 {
			t.Fatalf("after the redraw: screen %v, %d bytes drawn; want current and a draw", ed.screen, out.Len())
		}
		end.do(ed)
		out.Reset()
		ed.changed()
		ed.redraw()
		ed.render()
		if ed.screen != screenDone || out.Len() != 0 {
			t.Errorf("%s: screen %v, drew %q after; want done and nothing drawn", end.name, ed.screen, out.String())
		}
	}
}
