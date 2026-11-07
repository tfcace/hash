package editor

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// finishedFill gives ed an agent fill that has finished streaming.
func finishedFill(ed *Editor, text string) {
	updates := make(chan GhostStreamUpdate, 1)
	errs := make(chan error)
	ed.SetGhostTextStreaming(updates, errs, func() {})
	updates <- GhostStreamUpdate{Text: text}
	close(updates)
	close(errs)
}

// A multi-line agent fill shows its first line and how many more there
// are. Enter used to accept and run all of it, hidden lines included; it now
// puts the fill in the buffer, where every line shows, and only the next
// Enter runs it.
func TestRun_MultilineFillEnterShowsBeforeRunning(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	out := &screenBuf{}
	ed := New(Config{}, pr, out)
	ed.SetInitialText("ls ")
	finishedFill(ed, "-la\necho HIDDEN")

	results := make(chan Result, 1)
	go func() {
		res, _ := ed.Run(context.Background())
		results <- res
	}()
	eventually(t, func() bool { return strings.Contains(out.lastFrame(), "+1 line") }, "no marker for the hidden line")
	if strings.Contains(out.String(), "HIDDEN") {
		t.Fatalf("the second line is drawn after the cursor: %q", out.String())
	}

	pw.WriteString("\r")
	eventually(t, func() bool { return strings.Contains(out.lastFrame(), "echo HIDDEN") }, "Enter did not put the fill on screen")
	select {
	case res := <-results:
		t.Fatalf("Enter ran %q, lines the screen never showed", res.Text)
	case <-time.After(100 * time.Millisecond):
	}

	pw.WriteString("\r")
	if res := <-results; res.Exit != ExitSubmit || res.Text != "ls -la\necho HIDDEN" {
		t.Errorf("second Enter: %q (exit %v)", res.Text, res.Exit)
	}
}

// A one-line fill is shown whole: Enter accepts and runs it.
func TestGhost_OneLineFillEnterRuns(t *testing.T) {
	ed := newKeyEditor(t, Config{}, "ls ")
	finishedFill(ed, "-la")
	drainGhostStream(ed)
	res, done := press(t, ed, Key{Special: KeyEnter})
	if !done || res.Text != "ls -la" {
		t.Fatalf("Enter = %q done=%v", res.Text, done)
	}
}

// drainGhostStream applies the attached stream's pending events the way the
// event loop would.
func drainGhostStream(ed *Editor) {
	for ed.ghost.streaming() {
		select {
		case u, ok := <-ed.ghost.updates:
			ed.handleGhostStreamUpdate(u, ok)
		case err, ok := <-ed.ghost.errs:
			ed.handleGhostTextError(err, ok)
		}
	}
}
