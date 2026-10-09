package editor

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// newInlineEditor mirrors the shell's inline ?? setup: a typed prefix with an
// agent ghost stream attached.
func newInlineEditor(t *testing.T, out *bytes.Buffer, gutter bool) *Editor {
	t.Helper()
	ed := New(Config{Keybindings: "emacs", Gutter: gutter}, strings.NewReader(""), out)
	ed.SetInitialText("date +")
	updates := make(chan GhostStreamUpdate)
	errCh := make(chan error, 1)
	ed.SetGhostTextStreaming(updates, errCh)
	ed.ghost.FromAgent = true
	return ed
}

func TestHandleGhostTextError_ShowsNoticeAndKeepsLine(t *testing.T) {
	var out bytes.Buffer
	ed := newInlineEditor(t, &out, false)
	out.Reset()

	ed.handleGhostTextError(errors.New("rpc error -32603: model missing"), true)

	if got := ed.state.Buffer.Content(); got != "date +" {
		t.Fatalf("buffer = %q, want the typed prefix kept", got)
	}
	if !strings.Contains(out.String(), "model missing") {
		t.Errorf("expected the agent error on screen, got:\n%q", out.String())
	}
	if ed.ghost.Streaming {
		t.Error("ghost should no longer be streaming after an error")
	}
}

func TestHandleGhostTextError_ShowsNoticeInGutterMode(t *testing.T) {
	var out bytes.Buffer
	ed := newInlineEditor(t, &out, true)
	out.Reset()

	ed.handleGhostTextError(errors.New("model missing"), true)

	if !strings.Contains(out.String(), "model missing") {
		t.Errorf("expected the agent error on screen in gutter mode, got:\n%q", out.String())
	}
}

func TestHandleGhostTextError_NoticeClearsOnNextKey(t *testing.T) {
	var out bytes.Buffer
	ed := newInlineEditor(t, &out, false)
	ed.handleGhostTextError(errors.New("model missing"), true)
	out.Reset()

	ed.handleKeyEvent(Key{Rune: '%'})

	if strings.Contains(out.String(), "model missing") {
		t.Errorf("notice should disappear on the next keypress, got:\n%q", out.String())
	}
	if got := ed.state.Buffer.Content(); got != "date +%" {
		t.Errorf("buffer = %q, want the key inserted", got)
	}
}

func TestHandleGhostTextError_TimeoutReadsAsTimedOut(t *testing.T) {
	var out bytes.Buffer
	ed := newInlineEditor(t, &out, false)
	out.Reset()

	ed.handleGhostTextError(context.DeadlineExceeded, true)

	if !strings.Contains(out.String(), "agent request timed out") {
		t.Errorf("expected a plain timeout notice, got:\n%q", out.String())
	}
}

func TestHandleGhostTextError_EnterSubmitsTypedLine(t *testing.T) {
	var out bytes.Buffer
	ed := newInlineEditor(t, &out, false)
	ed.handleGhostTextError(errors.New("model missing"), true)

	result, done := ed.handleKeyEvent(Key{Special: KeyEnter})

	if !done {
		t.Fatal("Enter after an agent error should submit the typed line")
	}
	if result.Text != "date +" {
		t.Errorf("Text = %q, want %q", result.Text, "date +")
	}
}
