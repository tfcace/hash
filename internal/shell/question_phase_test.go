package shell

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/editor"
)

// A line question's settle rule ends at its first settled key: a settled
// "y", Backspace, then "n⏎" typed at a person's pace (100ms apart) answers
// n. Emptying the line used to put the settle rule back, so the correction
// and its Enter were dropped, the second time with no note at all.
func TestAnswerLine_CorrectionAfterTheFirstSettledKeyAnswers(t *testing.T) {
	sh, typing := terminalShell(t)
	clock := typingAt(sh.editorCfg.Input)
	var out lockedBuffer
	got := answerAsync(t, sh, &out)
	clock.waitReadings(t, 1)
	clock.advance(settleGap)
	for i, k := range []string{"y", "\x7f", "n", "\r"} {
		typing.WriteString(k)
		clock.waitReadings(t, int64(i+2))
		clock.advance(100 * time.Millisecond)
	}
	select {
	case line := <-got:
		if line != "n" {
			t.Errorf("line %q, want n", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("y, Backspace, n, Enter did not answer: echoed %q", out.String())
	}
	if strings.Contains(out.String(), lineNote) {
		t.Errorf("echoed %q: the note was drawn for keys typed after a settled one", out.String())
	}
}

// The note a line question draws for a dropped key is erased once, by the
// answer's first character, and stays erased when the line is emptied and
// typed again.
func TestAnswerLine_NoteErasedOnce(t *testing.T) {
	sh, typing := terminalShell(t)
	clock := typingAt(sh.editorCfg.Input)
	var out lockedBuffer
	got := answerAsync(t, sh, &out)
	clock.waitReadings(t, 1)
	typing.WriteString("x") // too quick: dropped, with the note
	clock.waitReadings(t, 2)
	clock.advance(settleGap)
	for i, k := range []string{"y", "\x7f", "n", "\r"} {
		typing.WriteString(k)
		clock.waitReadings(t, int64(i+3))
	}
	select {
	case line := <-got:
		if line != "n" {
			t.Errorf("line %q, want n", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no answer: echoed %q", out.String())
	}
	if want := lineNote + "\x1b[Ky\b \bn\r\n"; out.String() != want {
		t.Errorf("echoed %q, want %q", out.String(), want)
	}
}

// A permission prompt claims its key before it is drawn. A key dropped in
// between (too quick) used to spend the prompt's one note while the note
// had no prompt to draw on (NoteTooQuick does nothing then), so no note
// was ever shown, for that key or a quick one after the draw. The note is
// owed until the prompt is drawn, and drawn then.
func TestStreamKeyWatcher_KeyDroppedBeforeTheDrawNotes(t *testing.T) {
	keys, _ := typedKeys(t)
	clock := typingAt(keys)
	w := newStreamKeyWatcher(keys, func() {})
	var onScreen atomic.Bool
	var notes atomic.Int32
	ask := w.Ask(func() { // as NoteTooQuick: nothing before the prompt is drawn
		if onScreen.Load() {
			notes.Add(1)
		}
	})
	clock.advance(10 * time.Millisecond)
	w.key(editor.Key{Rune: 'y'}, clock.now(), time.Time{}) // dropped, the prompt not drawn yet
	onScreen.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan byte, 1)
	go func() { got <- ask.Wait(ctx) }()
	waitFor(t, func() bool { return notes.Load() == 1 })
	at := clock.now()
	clock.advance(10 * time.Millisecond)
	w.key(editor.Key{Rune: 'y'}, clock.now(), at) // dropped again: the note is on screen
	cancel()
	if k := <-got; k != 'n' {
		t.Errorf("a dropped key answered the prompt: %q", k)
	}
	if n := notes.Load(); n != 1 {
		t.Errorf("the note was drawn %d times, want once", n)
	}
}

// The note goes after the question's cursor, saved (ESC 7) and restored
// (ESC 8) around it. On the bottom row of a narrow pane a note longer than
// the rest of the row wrapped, scrolling the screen, so ESC 8 put the
// cursor on the note's wrapped row: the answer echoed there and its first
// character erased only that row, leaving the note behind. Autowrap is off
// while the note draws: a long note is cut at the margin and never scrolls.
func TestLineNote_DoesNotWrap(t *testing.T) {
	save, text, restore := strings.Index(lineNote, "\x1b7"), strings.Index(lineNote, tooQuickNote), strings.Index(lineNote, "\x1b8")
	off, on := strings.Index(lineNote, "\x1b[?7l"), strings.Index(lineNote, "\x1b[?7h")
	inOrder := save >= 0 && save < off && off < text && text < on && on < restore
	if !inOrder {
		t.Errorf("lineNote %q: want ESC 7, autowrap off, the note, autowrap on, ESC 8", lineNote)
	}
}

// A line question that ends while its note shows (a settled Enter, the
// default, or Esc, Ctrl+C, Ctrl+D) takes the note off the answered row
// before the newline: only the answer's first character used to erase it.
func TestAnswerLine_NoteErasedWhenTheLineEnds(t *testing.T) {
	for _, final := range []string{"\r", "\x1b", "\x03", "\x04"} {
		sh, typing := terminalShell(t)
		clock := typingAt(sh.editorCfg.Input)
		var out lockedBuffer
		got := answerAsync(t, sh, &out)
		clock.waitReadings(t, 1)
		typing.WriteString("x") // too quick: dropped, with the note
		clock.waitReadings(t, 2)
		clock.advance(settleGap)
		typing.WriteString(final)
		select {
		case <-got:
		case <-time.After(2 * time.Second):
			t.Fatalf("%q: no answer: echoed %q", final, out.String())
		}
		if want := lineNote + "\x1b[K\r\n"; out.String() != want {
			t.Errorf("%q ended the line: echoed %q, want %q", final, out.String(), want)
		}
	}
}
