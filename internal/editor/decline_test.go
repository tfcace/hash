package editor

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

// In an editor Esc declines (the agent's command or inline fill), an open
// completion menu or a history suggestion takes Esc first, as at the
// prompt: Esc closing a menu used to discard the line and end the turn.
// The agent's own fill is what Esc declines, streaming or not.
func TestEditor_EscClosesWhatIsOpenBeforeDeclining(t *testing.T) {
	cfg := Config{
		Decline:             DeclineKeepRow,
		CompleteOutcomeFunc: itemsOnly(fakeTree("src/", "src/a.go", "lib/")),
	}

	ed := newKeyEditor(t, cfg, "cat ")
	press(t, ed, keyTab())
	if ed.menu == nil {
		t.Fatal("no menu")
	}
	if _, done := press(t, ed, keyEsc()); done {
		t.Fatal("menu: Esc declined the line, want the menu closed")
	}
	if ed.menu != nil || ed.state.Buffer.Content() != "cat " {
		t.Errorf("menu: open=%v buffer %q; want the menu closed and the line kept", ed.menu != nil, ed.state.Buffer.Content())
	}
	if res, done := press(t, ed, keyEsc()); !done || res.Exit != ExitCanceled {
		t.Errorf("menu: a second Esc gave done=%v exit %v, want the line declined", done, res.Exit)
	}

	ed = newKeyEditor(t, cfg, "git ")
	ed.SetGhostText("status")
	if _, done := press(t, ed, keyEsc()); done {
		t.Fatal("history suggestion: Esc declined the line, want the suggestion dismissed")
	}
	if ed.ghost.visible() || ed.state.Buffer.Content() != "git " {
		t.Errorf("history suggestion: ghost=%v buffer %q; want it gone and the line kept", ed.ghost.visible(), ed.state.Buffer.Content())
	}

	for _, streaming := range []bool{true, false} {
		ed = newKeyEditor(t, cfg, "ls ")
		setAgentGhost(ed.ghost, "-la", streaming)
		if res, done := press(t, ed, keyEsc()); !done || res.Exit != ExitCanceled {
			t.Errorf("agent fill (streaming %v): Esc gave done=%v exit %v, want the fill declined", streaming, done, res.Exit)
		}
	}
}

// A line declined where the row is kept stays on screen as typed, without
// the agent's fill, and its last row is ended: the next prompt drew over it
// in terminals that do not start a fresh line on their own, and over its
// second half when it wrapped.
func TestEditor_DeclineKeepRowEndsTheRow(t *testing.T) {
	for _, key := range []string{"\x1b", "\x03"} {
		var out bytes.Buffer
		ed := New(Config{Decline: DeclineKeepRow}, strings.NewReader(key), &out)
		ed.SetInitialText("echo a\necho b") // the cursor on the first of two rows
		setAgentGhost(ed.ghost, " FILL", false)
		res, err := ed.Run(context.Background())
		if err != nil || res.Exit != ExitCanceled {
			t.Fatalf("%q: Run() = %#v, %v; want declined", key, res, err)
		}
		got := out.String()
		if !strings.HasSuffix(got, fmt.Sprintf(ansiCursorDown, 1)+"\r\n") {
			t.Errorf("%q: output ends %q; want it to move past the last row and end it", key, got[max(0, len(got)-40):])
		}
		last := got[strings.LastIndex(got, ansiHideCursor):]
		if strings.Contains(last, "FILL") || !strings.Contains(last, "echo b") {
			t.Errorf("%q: last draw %q; want the line as typed, without the fill", key, last)
		}
	}
}
