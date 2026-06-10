package editor

import (
	"io"
	"strings"
	"testing"
)

// Enter on a finished agent ghost must accept the fill and submit the
// completed line, matching the "[enter]run" hint.
func TestHandleKeyEvent_EnterRunsAgentGhostCompletion(t *testing.T) {
	ed := New(Config{Keybindings: "emacs"}, strings.NewReader(""), io.Discard)
	ed.SetInitialText("git log --format=")
	ed.ghost.Set("'%h %s'")
	ed.ghost.FromAgent = true

	result, done := ed.handleKeyEvent(Key{Special: KeyEnter})

	if !done {
		t.Fatal("expected Enter to submit the line")
	}
	if want := "git log --format='%h %s'"; result.Text != want {
		t.Errorf("Text = %q, want %q", result.Text, want)
	}
}

// While the agent completion is still streaming there is no run hint,
// so Enter must not submit a partial or bare command.
func TestHandleKeyEvent_EnterDuringAgentStreamingIsNoOp(t *testing.T) {
	ed := New(Config{Keybindings: "emacs"}, strings.NewReader(""), io.Discard)
	ed.SetInitialText("git log --format=")
	ed.ghost.Set("'%h")
	ed.ghost.FromAgent = true
	ed.ghost.SetStreaming(true)

	_, done := ed.handleKeyEvent(Key{Special: KeyEnter})

	if done {
		t.Fatal("Enter during streaming must not submit")
	}
	if got := ed.state.Buffer.Content(); got != "git log --format=" {
		t.Errorf("buffer = %q, want unchanged prefix", got)
	}
	if !ed.ghost.Active {
		t.Error("ghost should remain active while streaming")
	}
}

// History predictions keep fish-shell semantics: Enter dismisses the
// ghost and submits only what was typed.
func TestHandleKeyEvent_EnterDismissesPredictionGhost(t *testing.T) {
	ed := New(Config{Keybindings: "emacs"}, strings.NewReader(""), io.Discard)
	ed.SetInitialText("git sta")
	ed.ghost.Set("tus")

	result, done := ed.handleKeyEvent(Key{Special: KeyEnter})

	if !done {
		t.Fatal("expected Enter to submit the line")
	}
	if want := "git sta"; result.Text != want {
		t.Errorf("Text = %q, want %q", result.Text, want)
	}
}
