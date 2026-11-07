package editor

import (
	"context"
	"io"
	"testing"
	"time"
)

// A prompt torn down mid-paste (the agent fill ended, a resize redrew the
// prompt): the reader used to stop between the paste's chunks, so the tail
// after it came as typed keys and its newline ran "echo second". The paste
// is now read to its end marker and handed to the next prompt whole.
func TestEditors_PasteInterruptedByTeardown(t *testing.T) {
	master, slave := openPty(t)
	r := NewInputReader(slave)

	ctx, cancel := context.WithCancel(context.Background())
	ed := New(Config{Input: r, OnInputReady: func() {
		go func() {
			master.WriteString("\x1b[200~echo first\r")
			time.Sleep(100 * time.Millisecond)
			cancel()
			time.Sleep(100 * time.Millisecond)
			master.WriteString("echo second\r\x1b[201~")
		}()
	}}, slave, io.Discard)
	if res, _ := ed.Run(ctx); res.Exit != ExitCanceled {
		t.Fatalf("first prompt exit = %v", res.Exit)
	}

	res, next, _ := runPrompt(t, r, slave, 300*time.Millisecond, nil)
	if res.Exit == ExitSubmit {
		t.Fatalf("next prompt ran %q: the paste's tail was typed", res.Text)
	}
	if got, want := next.state.Buffer.Content(), "echo first\necho second\n"; got != want {
		t.Fatalf("next prompt holds %q, want the whole paste %q", got, want)
	}
}

// A paste whose end marker never comes ends after pasteIdleTimeout with what
// arrived; the late rest is dropped through the marker, not typed.
func TestInputReader_StalledPasteIsCutAndItsRestDropped(t *testing.T) {
	r, w := pipeReader(t)
	done := make(chan struct{})
	defer close(done)
	w.WriteString("\x1b[200~ab")
	start := time.Now()
	k, err := r.ReadKeyInterruptible(done)
	if err != nil || k.Special != KeyPaste || k.PasteText != "ab" {
		t.Fatalf("stalled paste = %+v, %v; want Paste(\"ab\")", k, err)
	}
	if waited := time.Since(start); waited < pasteIdleTimeout/2 || waited > 3*pasteIdleTimeout {
		t.Errorf("stalled paste ended after %v, want about %v", waited, pasteIdleTimeout)
	}
	w.WriteString("cd\r\x1b[201~x")
	if k, err := r.ReadKeyInterruptible(done); err != nil || k != (Key{Rune: 'x'}) {
		t.Fatalf("after the late rest: %+v, %v; want x", k, err)
	}
}

// When the rest of a cut paste never comes either, typing resumes after a
// pause rather than being dropped while waiting for a marker.
func TestInputReader_CutPasteWithoutMarkerResumesTyping(t *testing.T) {
	r, w := pipeReader(t)
	done := make(chan struct{})
	defer close(done)
	w.WriteString("\x1b[200~ab")
	if k, _ := r.ReadKeyInterruptible(done); k.PasteText != "ab" {
		t.Fatalf("paste = %+v", k)
	}
	got := readAsync(r, done)
	w.WriteString("cd")
	time.Sleep(2 * pasteIdleTimeout)
	w.WriteString("x")
	select {
	case res := <-got:
		if res.err != nil || res.key != (Key{Rune: 'x'}) {
			t.Fatalf("typing after the pause = %+v, %v; want x", res.key, res.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("typing after the pause never arrived")
	}
}
