package editor

import (
	"strings"
	"testing"
)

func TestGhostText_Set(t *testing.T) {
	g := NewGhostText()

	g.Set("hello world")

	if g.state != ghostPrediction {
		t.Errorf("state = %v, want prediction", g.state)
	}
	if g.text != "hello world" {
		t.Errorf("expected Text to be 'hello world', got %q", g.text)
	}
	if g.accepted != 0 {
		t.Errorf("expected AcceptedAt to be 0, got %d", g.accepted)
	}
}

func TestGhostText_StreamUpdates(t *testing.T) {
	g := NewGhostText()
	g.startStream(make(chan GhostStreamUpdate), nil, nil)

	g.update(GhostStreamUpdate{Text: "hello"})
	g.update(GhostStreamUpdate{Text: " world", Status: "running"})

	if g.text != "hello world" || g.status != "running" {
		t.Errorf("Text, Status = %q, %q", g.text, g.status)
	}
	if !g.streaming() || g.updates == nil {
		t.Error("expected the stream to stay attached")
	}

	g.endStream()
	if g.state != ghostAgentDone || g.updates != nil || g.status != "" {
		t.Errorf("after endStream: state=%v updates=%v status=%q", g.state, g.updates, g.status)
	}

	// A detached ghost ignores late chunks.
	g.update(GhostStreamUpdate{Text: "!"})
	if g.text != "hello world" {
		t.Errorf("late chunk applied: %q", g.text)
	}
}

func TestGhostText_EmptyStreamEndsInNone(t *testing.T) {
	g := NewGhostText()
	g.startStream(make(chan GhostStreamUpdate), nil, nil)
	g.endStream()
	if g.state != ghostNone {
		t.Errorf("state = %v, want none for a stream that produced nothing", g.state)
	}
}

func TestGhostText_FailCarriesNotice(t *testing.T) {
	g := NewGhostText()
	g.startStream(make(chan GhostStreamUpdate), make(chan error), nil)
	g.update(GhostStreamUpdate{Text: "partial"})
	g.fail("agent: boom")
	if g.state != ghostAgentFailed || g.notice != "agent: boom" || g.text != "" || g.updates != nil || g.errs != nil {
		t.Errorf("after fail: %+v", g)
	}
	if g.visible() {
		t.Error("a failed ghost must not intercept keys")
	}
	g.startStream(make(chan GhostStreamUpdate), nil, nil)
	g.fail("")
	if g.state != ghostNone {
		t.Errorf("a canceled stream should leave nothing, got %v", g.state)
	}
}

func TestGhostText_Clear(t *testing.T) {
	g := NewGhostText()
	g.startStream(make(chan GhostStreamUpdate), make(chan error), nil)
	g.update(GhostStreamUpdate{Text: "hello", Status: "Agent running pwd..."})

	g.Clear()

	if g.state != ghostNone || g.updates != nil || g.errs != nil {
		t.Errorf("Clear left state=%v updates=%v errs=%v", g.state, g.updates, g.errs)
	}
	if g.text != "" {
		t.Errorf("expected Text to be empty, got %q", g.text)
	}
	if g.status != "" {
		t.Errorf("expected Status to be empty, got %q", g.status)
	}
}

func TestGhostText_Remaining(t *testing.T) {
	g := NewGhostText()
	g.Set("hello world")

	if g.Remaining() != "hello world" {
		t.Errorf("expected Remaining to be 'hello world', got %q", g.Remaining())
	}

	// Simulate partial acceptance
	g.accepted = 6

	if g.Remaining() != "world" {
		t.Errorf("expected Remaining to be 'world', got %q", g.Remaining())
	}
}

func TestGhostText_AcceptAll(t *testing.T) {
	g := NewGhostText()
	g.Set("hello world")

	text := g.AcceptAll()

	if text != "hello world" {
		t.Errorf("expected 'hello world', got %q", text)
	}
	if g.state != ghostNone {
		t.Error("expected the ghost cleared after AcceptAll")
	}
	if g.text != "" {
		t.Errorf("expected Text to be empty after AcceptAll, got %q", g.text)
	}
}

func TestGhostText_AcceptWord(t *testing.T) {
	g := NewGhostText()
	g.Set("hello world test")

	// Accept first word
	text := g.AcceptWord()
	if text != "hello " {
		t.Errorf("expected 'hello ', got %q", text)
	}

	// Accept second word
	text = g.AcceptWord()
	if text != "world " {
		t.Errorf("expected 'world ', got %q", text)
	}

	// Accept last word
	text = g.AcceptWord()
	if text != "test" {
		t.Errorf("expected 'test', got %q", text)
	}

	if g.Remaining() != "" {
		t.Error("expected nothing left to accept")
	}
}

func TestGhostText_StreamingAcceptKeepsStream(t *testing.T) {
	g := NewGhostText()
	g.startStream(make(chan GhostStreamUpdate), nil, nil)
	g.update(GhostStreamUpdate{Text: "ls"})

	if got := g.AcceptWord(); got != "ls" {
		t.Fatalf("AcceptWord = %q", got)
	}
	if !g.streaming() {
		t.Error("accepting everything received so far must keep the stream attached")
	}
	if got := g.AcceptWord(); got != "" || !g.streaming() {
		t.Errorf("AcceptWord on a caught-up stream = %q, streaming=%v", got, g.streaming())
	}
}

func TestEditor_NonAgentGhostRendersBare(t *testing.T) {
	// Predictions and learned fixes render fish-style: just the dim
	// suggestion, no key hints on the input line.
	var out strings.Builder
	ed := New(Config{}, strings.NewReader(""), &out)
	ed.SetGhostText("git checkout master")
	ed.render()

	got := out.String()
	if !strings.Contains(got, "git checkout master") {
		t.Fatalf("render missing ghost text: %q", got)
	}
	if strings.Contains(got, "accept") || strings.Contains(got, "dismiss") {
		t.Errorf("non-agent ghost must render without key hints: %q", got)
	}
}

func TestGhostText_AcceptWordKeepsMultibyteRunes(t *testing.T) {
	g := NewGhostText()
	g.Set("wörld café")

	if got := g.AcceptWord(); got != "wörld " {
		t.Fatalf("AcceptWord = %q, want %q", got, "wörld ")
	}
	if got := g.AcceptWord(); got != "café" {
		t.Fatalf("AcceptWord = %q, want %q", got, "café")
	}
}

// A ghost ending in an invalid byte (a history entry or agent text cut mid
// rune) is accepted byte for byte: the range loop's RuneError counted as
// three bytes and sliced past the end ("slice bounds out of range [:6] with
// length 4"), panicking on Alt+Tab.
func TestGhostText_AcceptWordWithInvalidUTF8(t *testing.T) {
	for _, text := range []string{"caf\xe9", "caf\xe9 x", "\xff", "a\xc3"} {
		g := NewGhostText()
		g.Set(text)
		var got string
		for range 4 {
			got += g.AcceptWord()
		}
		if got != text {
			t.Errorf("AcceptWord over %q accepted %q", text, got)
		}
	}
}

// The same through the editor: Alt+Tab on a prediction from SuggestionFunc.
func TestEditor_AltTabOnInvalidUTF8Prediction(t *testing.T) {
	ed := newKeyEditor(t, Config{SuggestionFunc: func(string) string { return "echo caf\xe9" }}, "")
	press(t, ed, runes("echo ")...)
	press(t, ed, Key{Special: KeyTab, Alt: true})
	if got := ed.state.Buffer.Content(); got != "echo caf\xe9" {
		t.Fatalf("buffer = %q, want %q", got, "echo caf\xe9")
	}
}

// A newline ends a word: Alt+Tab on a multi-line fill takes the first
// line's last word up to its newline, never the next line's word with it.
func TestGhostText_AcceptWordStopsAtNewline(t *testing.T) {
	g := NewGhostText()
	g.Set("ls\nwc -l")
	if got := g.AcceptWord(); got != "ls\n" {
		t.Errorf("first word = %q, want \"ls\\n\"", got)
	}
	if got := g.AcceptWord(); got != "wc " {
		t.Errorf("second word = %q, want \"wc \"", got)
	}
}
