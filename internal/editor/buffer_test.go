// internal/editor/buffer_test.go
package editor

import (
	"testing"
)

func TestBuffer_NewEmpty(t *testing.T) {
	buf := NewBuffer()
	if buf.LineCount() != 1 {
		t.Errorf("LineCount() = %d, want 1", buf.LineCount())
	}
	if buf.Content() != "" {
		t.Errorf("Content() = %q, want empty", buf.Content())
	}
}

func TestBuffer_NewWithContent(t *testing.T) {
	buf := NewBufferFromString("hello\nworld")
	if buf.LineCount() != 2 {
		t.Errorf("LineCount() = %d, want 2", buf.LineCount())
	}
	if buf.Line(0) != "hello" {
		t.Errorf("Line(0) = %q, want %q", buf.Line(0), "hello")
	}
	if buf.Line(1) != "world" {
		t.Errorf("Line(1) = %q, want %q", buf.Line(1), "world")
	}
}

func TestBuffer_Insert(t *testing.T) {
	buf := NewBuffer()
	buf.Insert(0, 0, "hello")
	if buf.Content() != "hello" {
		t.Errorf("Content() = %q, want %q", buf.Content(), "hello")
	}

	buf.Insert(0, 5, " world")
	if buf.Content() != "hello world" {
		t.Errorf("Content() = %q, want %q", buf.Content(), "hello world")
	}
}

func TestBuffer_InsertNewline(t *testing.T) {
	buf := NewBufferFromString("hello world")
	buf.Insert(0, 5, "\n")
	if buf.LineCount() != 2 {
		t.Errorf("LineCount() = %d, want 2", buf.LineCount())
	}
	if buf.Line(0) != "hello" {
		t.Errorf("Line(0) = %q, want %q", buf.Line(0), "hello")
	}
	if buf.Line(1) != " world" {
		t.Errorf("Line(1) = %q, want %q", buf.Line(1), " world")
	}
}

func TestBuffer_Delete(t *testing.T) {
	buf := NewBufferFromString("hello world")
	buf.Delete(Position{0, 5}, Position{0, 11})
	if buf.Content() != "hello" {
		t.Errorf("Content() = %q, want %q", buf.Content(), "hello")
	}
}

func TestBuffer_DeleteAcrossLines(t *testing.T) {
	buf := NewBufferFromString("hello\nworld")
	buf.Delete(Position{0, 3}, Position{1, 2})
	if buf.Content() != "helrld" {
		t.Errorf("Content() = %q, want %q", buf.Content(), "helrld")
	}
}

func TestBuffer_Clone(t *testing.T) {
	buf := NewBufferFromString("hello")
	clone := buf.Clone()
	clone.Insert(0, 5, " world")

	if buf.Content() != "hello" {
		t.Errorf("Original modified: %q", buf.Content())
	}
	if clone.Content() != "hello world" {
		t.Errorf("Clone not modified: %q", clone.Content())
	}
}
