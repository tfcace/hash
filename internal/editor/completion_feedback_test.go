package editor

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestEditor_TabOnAgentLineSubmitsForCompletion(t *testing.T) {
	line := "git log --since ?? last tuesday"
	cfg := Config{
		AgentCompleteLine: func(l string) bool { return strings.Contains(l, "??") },
		CompleteFunc: func(string, int) []Completion {
			t.Error("regular completion must not run for agent lines")
			return nil
		},
	}
	ed := New(cfg, strings.NewReader(""), io.Discard)
	ed.state.Buffer = NewBufferFromString(line)
	ed.state.Cursor.MoveTo(0, len(line))

	result, done := ed.handleKeyEvent(Key{Special: KeyTab})
	if !done {
		t.Fatal("Tab on an inline ?? line should end the session to start agent completion")
	}
	if result.Text != line {
		t.Errorf("Result.Text = %q, want the full line %q", result.Text, line)
	}
}

func TestEditor_TabOnPlainLineIgnoresAgentDetector(t *testing.T) {
	cfg := Config{
		AgentCompleteLine: func(l string) bool { return strings.Contains(l, "??") },
		CompleteFunc: func(string, int) []Completion {
			return []Completion{{Text: "test1"}, {Text: "test2"}}
		},
	}
	ed := New(cfg, strings.NewReader(""), io.Discard)
	ed.state.Buffer = NewBufferFromString("ls foo")
	ed.state.Cursor.MoveTo(0, 6)

	_, done := ed.handleKeyEvent(Key{Special: KeyTab})
	if done {
		t.Fatal("Tab on a plain line must not submit")
	}
	if !ed.completionActive {
		t.Error("regular completion menu should be active")
	}
}

func TestEditor_NoCompletionsShowsNotice(t *testing.T) {
	var out bytes.Buffer
	cfg := Config{CompleteFunc: func(string, int) []Completion { return nil }}
	ed := New(cfg, strings.NewReader(""), &out)
	ed.state.Buffer = NewBufferFromString("ls zzz")
	ed.state.Cursor.MoveTo(0, 6)

	ed.handleKeyEvent(Key{Special: KeyTab})
	if ed.completionNotice == "" {
		t.Fatal("expected a visible notice when completion returns nothing")
	}
	if !strings.Contains(out.String(), ed.completionNotice) {
		t.Errorf("notice %q should be rendered to the terminal", ed.completionNotice)
	}

	// The next keypress clears the notice.
	ed.handleKeyEvent(Key{Rune: 'a'})
	if ed.completionNotice != "" {
		t.Error("notice should clear on the next key")
	}
}

func TestEditor_TimedOutCompletionShowsTimeoutNotice(t *testing.T) {
	cfg := Config{
		CompleteOutcomeFunc: func(string, int) CompletionOutcome {
			return CompletionOutcome{TimedOut: true}
		},
	}
	ed := New(cfg, strings.NewReader(""), io.Discard)
	ed.state.Buffer = NewBufferFromString("git ch")
	ed.state.Cursor.MoveTo(0, 6)

	ed.handleKeyEvent(Key{Special: KeyTab})
	if !strings.Contains(ed.completionNotice, "timed out") {
		t.Errorf("notice = %q, want a timeout-specific message", ed.completionNotice)
	}
}

func TestEditor_CompleteOutcomeFuncSuppliesItems(t *testing.T) {
	cfg := Config{
		CompleteOutcomeFunc: func(string, int) CompletionOutcome {
			return CompletionOutcome{Items: []Completion{{Text: "alpha"}, {Text: "beta"}}}
		},
	}
	ed := New(cfg, strings.NewReader(""), io.Discard)
	ed.state.Buffer = NewBufferFromString("x")
	ed.state.Cursor.MoveTo(0, 1)

	ed.handleKeyEvent(Key{Special: KeyTab})
	if !ed.completionActive {
		t.Error("completion menu should activate from CompleteOutcomeFunc items")
	}
	if ed.completionNotice != "" {
		t.Errorf("no notice expected when items exist, got %q", ed.completionNotice)
	}
}
