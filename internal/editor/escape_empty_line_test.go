package editor

import (
	"io"
	"strings"
	"testing"
)

// A newcomer who presses Esc at an empty prompt and then types a command
// must see that command, not have its letters interpreted as motions.
func TestEditor_EscapeOnEmptyPromptThenTypingInsertsText(t *testing.T) {
	ed := New(Config{}, strings.NewReader(""), io.Discard)

	ed.handleKeyEvent(Key{Special: KeyEscape})
	for _, r := range "exit" {
		ed.handleKeyEvent(Key{Rune: r})
	}

	if got := ed.state.Buffer.Content(); got != "exit" {
		t.Errorf("buffer = %q, want %q", got, "exit")
	}
	if got := ed.mode.Name(); got != "insert" {
		t.Errorf("mode = %q, want insert", got)
	}
}

// Esc on a line with text still enters normal mode, so the editing
// vocabulary is unchanged once there is something to edit.
func TestEditor_EscapeOnTypedLineEntersNormalMode(t *testing.T) {
	ed := New(Config{}, strings.NewReader(""), io.Discard)

	for _, r := range "ls" {
		ed.handleKeyEvent(Key{Rune: r})
	}
	ed.handleKeyEvent(Key{Special: KeyEscape})

	if got := ed.mode.Name(); got != "normal" {
		t.Errorf("mode = %q, want normal", got)
	}
}
