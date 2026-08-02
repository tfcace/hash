package editor

import (
	"strings"
	"testing"
)

func TestGhostText_Set(t *testing.T) {
	g := NewGhostText()

	g.Set("hello world")

	if !g.Active {
		t.Error("expected Active to be true")
	}
	if g.Text != "hello world" {
		t.Errorf("expected Text to be 'hello world', got %q", g.Text)
	}
	if g.AcceptedAt != 0 {
		t.Errorf("expected AcceptedAt to be 0, got %d", g.AcceptedAt)
	}
}

func TestGhostText_Append(t *testing.T) {
	g := NewGhostText()

	g.Append("hello")
	g.Append(" world")

	if g.Text != "hello world" {
		t.Errorf("expected Text to be 'hello world', got %q", g.Text)
	}
	if !g.Active {
		t.Error("expected Active to be true")
	}
}

func TestGhostText_Clear(t *testing.T) {
	g := NewGhostText()
	g.Set("hello")
	g.SetStreaming(true)
	g.Status = "Agent running pwd..."

	g.Clear()

	if g.Active {
		t.Error("expected Active to be false")
	}
	if g.Streaming {
		t.Error("expected Streaming to be false")
	}
	if g.Text != "" {
		t.Errorf("expected Text to be empty, got %q", g.Text)
	}
	if g.Status != "" {
		t.Errorf("expected Status to be empty, got %q", g.Status)
	}
}

func TestGhostText_Remaining(t *testing.T) {
	g := NewGhostText()
	g.Set("hello world")

	if g.Remaining() != "hello world" {
		t.Errorf("expected Remaining to be 'hello world', got %q", g.Remaining())
	}

	// Simulate partial acceptance
	g.AcceptedAt = 6

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
	if g.Active {
		t.Error("expected Active to be false after AcceptAll")
	}
	if g.Text != "" {
		t.Errorf("expected Text to be empty after AcceptAll, got %q", g.Text)
	}
}

func TestGhostText_AcceptChar(t *testing.T) {
	g := NewGhostText()
	g.Set("hello")

	// Accept first character
	text := g.AcceptChar()
	if text != "h" {
		t.Errorf("expected 'h', got %q", text)
	}
	if g.Remaining() != "ello" {
		t.Errorf("expected Remaining to be 'ello', got %q", g.Remaining())
	}

	// Accept remaining characters
	for _, expected := range "ello" {
		char := g.AcceptChar()
		if char != string(expected) {
			t.Errorf("expected %q, got %q", string(expected), char)
		}
	}

	// Should be empty now
	if !g.IsEmpty() {
		t.Error("expected IsEmpty to be true")
	}
	if g.Active {
		t.Error("expected Active to be false")
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

	if !g.IsEmpty() {
		t.Error("expected IsEmpty to be true")
	}
}

func TestGhostText_IsEmpty(t *testing.T) {
	g := NewGhostText()

	if !g.IsEmpty() {
		t.Error("expected IsEmpty to be true for new ghost")
	}

	g.Set("hello")
	if g.IsEmpty() {
		t.Error("expected IsEmpty to be false after Set")
	}

	g.AcceptedAt = 5
	if !g.IsEmpty() {
		t.Error("expected IsEmpty to be true after accepting all")
	}
}

func TestGhostText_SetStreaming(t *testing.T) {
	g := NewGhostText()

	g.SetStreaming(true)

	if !g.Streaming {
		t.Error("expected Streaming to be true")
	}
	if !g.Active {
		t.Error("expected Active to be true when streaming")
	}
}

func TestGhostText_UTF8(t *testing.T) {
	g := NewGhostText()
	g.Set("héllo wörld")

	// Accept first character (multi-byte)
	text := g.AcceptChar()
	if text != "h" {
		t.Errorf("expected 'h', got %q", text)
	}

	text = g.AcceptChar()
	if text != "é" {
		t.Errorf("expected 'é', got %q", text)
	}

	if g.Remaining() != "llo wörld" {
		t.Errorf("expected 'llo wörld', got %q", g.Remaining())
	}
}

func TestEditor_NonAgentGhostRendersBare(t *testing.T) {
	// Predictions and learned fixes render fish-style: just the dim
	// suggestion, no key hints on the input line.
	var out strings.Builder
	ed := New(Config{Keybindings: "emacs"}, strings.NewReader(""), &out)
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
