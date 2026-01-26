package integration

import (
	"bytes"
	"testing"
)

// OSC 133 shell integration tests

func TestEmitter_PromptStart(t *testing.T) {
	var buf bytes.Buffer
	e := NewWithWriter(&buf)

	e.PromptStart()

	expected := "\x1b]133;A\x07"
	if buf.String() != expected {
		t.Errorf("PromptStart() = %q, want %q", buf.String(), expected)
	}
}

func TestEmitter_CommandStart(t *testing.T) {
	var buf bytes.Buffer
	e := NewWithWriter(&buf)

	e.CommandStart()

	expected := "\x1b]133;B\x07"
	if buf.String() != expected {
		t.Errorf("CommandStart() = %q, want %q", buf.String(), expected)
	}
}

func TestEmitter_CommandExecuted(t *testing.T) {
	var buf bytes.Buffer
	e := NewWithWriter(&buf)

	e.CommandExecuted()

	expected := "\x1b]133;C\x07"
	if buf.String() != expected {
		t.Errorf("CommandExecuted() = %q, want %q", buf.String(), expected)
	}
}

func TestEmitter_CommandFinished(t *testing.T) {
	var buf bytes.Buffer
	e := NewWithWriter(&buf)

	e.CommandFinished(0)

	expected := "\x1b]133;D;0\x07"
	if buf.String() != expected {
		t.Errorf("CommandFinished(0) = %q, want %q", buf.String(), expected)
	}
}

func TestEmitter_CommandFinished_NonZero(t *testing.T) {
	var buf bytes.Buffer
	e := NewWithWriter(&buf)

	e.CommandFinished(127)

	expected := "\x1b]133;D;127\x07"
	if buf.String() != expected {
		t.Errorf("CommandFinished(127) = %q, want %q", buf.String(), expected)
	}
}

// OSC 7 directory tests

func TestEmitter_ReportDirectory(t *testing.T) {
	var buf bytes.Buffer
	e := NewWithWriter(&buf)

	e.ReportDirectory("/Users/test/projects")

	got := buf.String()
	// Should contain OSC 7 with file:// URL
	if !bytes.Contains(buf.Bytes(), []byte("\x1b]7;file://")) {
		t.Errorf("ReportDirectory() should emit OSC 7, got %q", got)
	}
	if !bytes.Contains(buf.Bytes(), []byte("/Users/test/projects\x07")) {
		t.Errorf("ReportDirectory() should contain path, got %q", got)
	}
}
