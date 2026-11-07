package editor

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// setAgentGhost shows text as an agent fill, still streaming or finished.
func setAgentGhost(g *GhostText, text string, streaming bool) {
	g.startStream(make(chan GhostStreamUpdate), nil, nil)
	g.update(GhostStreamUpdate{Text: text})
	if !streaming {
		g.endStream()
	}
}

// newStreamingEditor attaches an agent ghost stream to a typed prefix, the way
// the shell sets up inline ?? completion.
func newStreamingEditor(t *testing.T, cfg Config, prefix string) (*Editor, chan GhostStreamUpdate, chan error) {
	t.Helper()
	ed := newKeyEditor(t, cfg, prefix)
	updates := make(chan GhostStreamUpdate, 4)
	errs := make(chan error, 1)
	ed.SetGhostTextStreaming(updates, errs, nil)
	return ed, updates, errs
}

// closeStream is the producer finishing without an error: both channels
// close, in the order the shell's producer closes them.
func closeStream(ed *Editor) {
	ed.handleGhostStreamUpdate(GhostStreamUpdate{}, false)
	ed.handleGhostTextError(nil, false)
}

// Typing while the agent fill streams detaches the stream: the fill was for
// the old line, so later chunks must not land on the new line or on a
// history prediction.
func TestGhost_TypingMidStreamDetachesStream(t *testing.T) {
	ed, _, _ := newStreamingEditor(t, Config{
		SuggestionFunc: func(in string) string { return in + "HIST" },
	}, "git log")
	ed.handleGhostStreamUpdate(GhostStreamUpdate{Text: " --one"}, true)

	press(t, ed, runes("x")...)
	ed.handleGhostStreamUpdate(GhostStreamUpdate{Text: "line"}, true)

	if got := ed.ghost.Remaining(); got != "HIST" {
		t.Fatalf("ghost = %q, want the plain history prediction %q", got, "HIST")
	}
}

// After typing mid-stream, Ctrl+C acts on the edited line (clears it) rather
// than canceling the editor as if the agent were still running.
func TestGhost_CtrlCAfterTypingMidStreamClearsLine(t *testing.T) {
	ed, _, _ := newStreamingEditor(t, Config{}, "git log")
	ed.handleGhostStreamUpdate(GhostStreamUpdate{Text: " --one"}, true)

	press(t, ed, runes("x")...)
	if _, done := press(t, ed, Key{Rune: 'c', Ctrl: true}); done {
		t.Fatal("Ctrl+C canceled the editor; want it to clear the edited line")
	}
	if got := ed.state.Buffer.Content(); got != "" {
		t.Fatalf("buffer = %q, want cleared", got)
	}
}

// Alt+Tab on a stream that has produced nothing yet must keep the stream:
// later chunks are still the agent fill, so Enter accepts it instead of
// submitting the bare prefix.
func TestGhost_AcceptWordOnEmptyStreamKeepsStream(t *testing.T) {
	ed, _, _ := newStreamingEditor(t, Config{}, "git ")

	press(t, ed, Key{Special: KeyTab, Alt: true})
	ed.handleGhostStreamUpdate(GhostStreamUpdate{Text: "status"}, true)
	closeStream(ed)

	res, done := press(t, ed, Key{Special: KeyEnter})
	if !done || res.Text != "git status" {
		t.Fatalf("Enter = (%q, %v), want the accepted agent fill %q", res.Text, done, "git status")
	}
}

// Alt+Tab that consumes all text received so far keeps streaming too.
func TestGhost_AcceptWordCatchingUpWithStreamKeepsStream(t *testing.T) {
	ed, _, _ := newStreamingEditor(t, Config{}, "git ")
	ed.handleGhostStreamUpdate(GhostStreamUpdate{Text: "log"}, true)

	press(t, ed, Key{Special: KeyTab, Alt: true})
	ed.handleGhostStreamUpdate(GhostStreamUpdate{Text: " --oneline"}, true)
	closeStream(ed)

	res, done := press(t, ed, Key{Special: KeyEnter})
	if !done || res.Text != "git log --oneline" {
		t.Fatalf("Enter = (%q, %v), want %q", res.Text, done, "git log --oneline")
	}
}

// Tab while the fill still streams accepts what has arrived and keeps the
// stream: the rest of the fill still lands, instead of leaving a truncated
// command for Enter to run.
func TestGhost_TabMidStreamKeepsStream(t *testing.T) {
	ed, _, _ := newStreamingEditor(t, Config{}, "git ")
	ed.handleGhostStreamUpdate(GhostStreamUpdate{Text: "log"}, true)

	press(t, ed, keyTab())
	ed.handleGhostStreamUpdate(GhostStreamUpdate{Text: " --oneline"}, true)
	closeStream(ed)

	res, done := press(t, ed, Key{Special: KeyEnter})
	if !done || res.Text != "git log --oneline" {
		t.Fatalf("Enter = (%q, %v), want %q", res.Text, done, "git log --oneline")
	}
}

// The producer sends its error and closes both channels; the editor's select
// may see the updates close before it takes the error. The stream only ends
// when both channels are closed, so the error still lands and the partial
// fill is never offered as a finished command.
func TestGhost_ErrorAfterUpdatesCloseStillFails(t *testing.T) {
	ed, _, errs := newStreamingEditor(t, Config{}, "git ")
	ed.handleGhostStreamUpdate(GhostStreamUpdate{Text: "push origin"}, true)
	errs <- errors.New("connection reset")
	close(errs)

	ed.handleGhostStreamUpdate(GhostStreamUpdate{}, false)
	// What the event loop does next: read whatever the ghost still listens on.
	for ed.ghost.errs != nil {
		err, ok := <-ed.ghost.errs
		ed.handleGhostTextError(err, ok)
	}

	if ed.ghost.state != ghostAgentFailed {
		t.Fatalf("state = %v, want AgentFailed: the error was dropped and %q offered as finished", ed.ghost.state, ed.ghost.text)
	}
}

// Run with the chunk, the error and both closes all ready at once: every
// run must end failed (the reviewer's probe lost the error in 59/200).
func TestGhost_ErroredStreamNeverEndsAsFill(t *testing.T) {
	for i := range 200 {
		updates := make(chan GhostStreamUpdate, 1)
		errs := make(chan error, 1)
		updates <- GhostStreamUpdate{Text: "git push origin"}
		errs <- errors.New("connection reset")
		close(errs)
		close(updates)
		pr, pw := io.Pipe()
		ed := New(Config{}, pr, io.Discard)
		ed.SetInitialText("x ??")
		ed.SetGhostTextStreaming(updates, errs, nil)
		go func() { time.Sleep(5 * time.Millisecond); pw.Close() }()
		if _, err := ed.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if ed.ghost.state != ghostAgentFailed {
			t.Fatalf("run %d: state = %v, want AgentFailed", i, ed.ghost.state)
		}
	}
}

// Ending a stream takes both channels closing, in either order.
func TestGhostText_StreamEndsWhenBothChannelsClose(t *testing.T) {
	for _, updatesFirst := range []bool{true, false} {
		g := NewGhostText()
		g.startStream(make(chan GhostStreamUpdate), make(chan error), nil)
		g.update(GhostStreamUpdate{Text: "ls"})
		if updatesFirst {
			g.updatesClosed()
		} else {
			g.errsClosed()
		}
		if !g.streaming() {
			t.Fatalf("updatesFirst=%v: ended with one channel still open", updatesFirst)
		}
		if updatesFirst {
			g.errsClosed()
		} else {
			g.updatesClosed()
		}
		if g.state != ghostAgentDone || g.Remaining() != "ls" {
			t.Fatalf("updatesFirst=%v: state=%v remaining=%q, want AgentDone %q", updatesFirst, g.state, g.Remaining(), "ls")
		}
	}
}
