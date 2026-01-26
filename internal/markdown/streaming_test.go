package markdown

import (
	"strings"
	"testing"
)

func TestStreamingRenderer_BasicText(t *testing.T) {
	r := NewStreamingRenderer()

	result := r.Write("hello world\n")
	if result != "hello world\n" {
		t.Errorf("expected 'hello world\\n', got %q", result)
	}
}

func TestStreamingRenderer_Bold(t *testing.T) {
	r := NewStreamingRenderer()

	result := r.Write("this is **bold** text\n")

	if !strings.Contains(result, bold) {
		t.Error("expected bold formatting")
	}
	if !strings.Contains(result, "bold") {
		t.Error("expected 'bold' text")
	}
	if !strings.Contains(result, reset) {
		t.Error("expected reset after bold")
	}
}

func TestStreamingRenderer_InlineCode(t *testing.T) {
	r := NewStreamingRenderer()

	result := r.Write("use `fmt.Println` here\n")

	if !strings.Contains(result, dim+cyan) {
		t.Error("expected code formatting")
	}
	if !strings.Contains(result, "fmt.Println") {
		t.Error("expected code text")
	}
}

func TestStreamingRenderer_Headers(t *testing.T) {
	tests := []struct {
		input    string
		contains string
	}{
		{"# Title\n", magenta},
		{"## Subtitle\n", blue},
		{"### Section\n", cyan},
		{"#### Subsection\n", bold},
	}

	for _, tt := range tests {
		r := NewStreamingRenderer()
		result := r.Write(tt.input)

		if !strings.Contains(result, tt.contains) {
			t.Errorf("header %q: expected color %q in output", tt.input, tt.contains)
		}
		if !strings.Contains(result, bold) {
			t.Errorf("header %q: expected bold", tt.input)
		}
	}
}

func TestStreamingRenderer_UnorderedList(t *testing.T) {
	r := NewStreamingRenderer()

	result := r.Write("- first item\n")

	if !strings.Contains(result, "•") {
		t.Error("expected bullet character")
	}
	if !strings.Contains(result, cyan) {
		t.Error("expected cyan bullet")
	}
	if !strings.Contains(result, "first item") {
		t.Error("expected item text")
	}
}

func TestStreamingRenderer_OrderedList(t *testing.T) {
	r := NewStreamingRenderer()

	result := r.Write("1. first item\n2. second item\n")

	if !strings.Contains(result, cyan+"1."+reset) {
		t.Error("expected styled '1.'")
	}
	if !strings.Contains(result, "first item") {
		t.Error("expected item text")
	}
}

func TestStreamingRenderer_CodeBlock(t *testing.T) {
	r := NewStreamingRenderer()

	var result strings.Builder
	result.WriteString(r.Write("```go\n"))
	result.WriteString(r.Write("func main() {\n"))
	result.WriteString(r.Write("}\n"))
	result.WriteString(r.Write("```\n"))

	output := result.String()

	// Should have language header
	if !strings.Contains(output, "go") {
		t.Error("expected language in output")
	}
	// Code should be styled
	if !strings.Contains(output, dim+cyan) {
		t.Error("expected code styling")
	}
}

func TestStreamingRenderer_CodeBlockNoLanguage(t *testing.T) {
	r := NewStreamingRenderer()

	var result strings.Builder
	result.WriteString(r.Write("```\n"))
	result.WriteString(r.Write("plain code\n"))
	result.WriteString(r.Write("```\n"))

	output := result.String()

	// Code should still be styled
	if !strings.Contains(output, "plain code") {
		t.Error("expected code content")
	}
}

func TestStreamingRenderer_ChunkedInput(t *testing.T) {
	r := NewStreamingRenderer()

	// Simulate streaming in small chunks
	var result strings.Builder
	result.WriteString(r.Write("hello "))
	result.WriteString(r.Write("**wor"))
	result.WriteString(r.Write("ld**\n"))

	output := result.String()

	if !strings.Contains(output, bold) {
		t.Error("expected bold formatting even with chunked input")
	}
	if !strings.Contains(output, "world") {
		t.Error("expected 'world' text")
	}
}

func TestStreamingRenderer_Flush(t *testing.T) {
	r := NewStreamingRenderer()

	// Write without newline
	result := r.Write("incomplete line")
	if result != "" {
		t.Error("expected no output before newline")
	}

	// Flush should return the content
	flushed := r.Flush()
	if flushed != "incomplete line" {
		t.Errorf("expected 'incomplete line', got %q", flushed)
	}
}

func TestStreamingRenderer_Reset(t *testing.T) {
	r := NewStreamingRenderer()

	// Put renderer in various states
	r.Write("```go\n")
	r.Write("code\n")

	if !r.inCodeBlock {
		t.Error("expected to be in code block")
	}

	r.Reset()

	if r.inCodeBlock {
		t.Error("expected code block state to be reset")
	}
	if r.lineBuffer.Len() != 0 {
		t.Error("expected line buffer to be reset")
	}
}

func TestStreamingRenderer_NestedIndentedList(t *testing.T) {
	r := NewStreamingRenderer()

	result := r.Write("  - nested item\n")

	// Should preserve indentation
	if !strings.HasPrefix(result, "  ") {
		t.Error("expected preserved indentation")
	}
	if !strings.Contains(result, "•") {
		t.Error("expected bullet")
	}
}

func TestStreamingRenderer_BoldInList(t *testing.T) {
	r := NewStreamingRenderer()

	result := r.Write("- **important** item\n")

	if !strings.Contains(result, "•") {
		t.Error("expected bullet")
	}
	if !strings.Contains(result, bold) {
		t.Error("expected bold in list item")
	}
}

func TestStreamingRenderer_SingleAsteriskLiteral(t *testing.T) {
	r := NewStreamingRenderer()

	result := r.Write("use * for multiplication\n")

	// Single asterisk should be literal in MVP
	if !strings.Contains(result, "*") {
		t.Error("expected literal asterisk")
	}
}

func TestStreamingRenderer_MultipleLines(t *testing.T) {
	r := NewStreamingRenderer()

	result := r.Write("# Header\n\nParagraph with **bold**.\n\n- item 1\n- item 2\n")

	// Should have header
	if !strings.Contains(result, magenta) {
		t.Error("expected header formatting")
	}
	// Should have bold
	if !strings.Contains(result, bold) {
		t.Error("expected bold formatting")
	}
	// Should have bullets
	if strings.Count(result, "•") != 2 {
		t.Errorf("expected 2 bullets, got %d", strings.Count(result, "•"))
	}
}
