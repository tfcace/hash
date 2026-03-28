package markdown

import (
	"strings"
	"testing"
)

func TestRender_Bold(t *testing.T) {
	tests := []struct {
		input    string
		wantBold bool
	}{
		{"**bold text**", true},
		{"__also bold__", true},
		{"normal text", false},
	}

	for _, tt := range tests {
		result := Render(tt.input)
		hasBold := strings.Contains(result, bold)
		if hasBold != tt.wantBold {
			t.Errorf("Render(%q) bold=%v, want %v", tt.input, hasBold, tt.wantBold)
		}
	}
}

func TestRender_Italic(t *testing.T) {
	tests := []struct {
		input      string
		wantItalic bool
	}{
		{"*italic text*", true},
		{"normal text", false},
	}

	for _, tt := range tests {
		result := Render(tt.input)
		hasItalic := strings.Contains(result, italic)
		if hasItalic != tt.wantItalic {
			t.Errorf("Render(%q) italic=%v, want %v", tt.input, hasItalic, tt.wantItalic)
		}
	}
}

func TestRender_InlineCode(t *testing.T) {
	input := "Use `go build` to compile"
	result := Render(input)

	if !strings.Contains(result, cyan) {
		t.Error("Inline code should be cyan")
	}
	if !strings.Contains(result, "go build") {
		t.Error("Code content should be preserved")
	}
	if strings.Contains(result, "`") {
		t.Error("Backticks should be removed")
	}
}

func TestRender_CodeBlock(t *testing.T) {
	input := "```go\nfunc main() {}\n```"
	result := Render(input)

	if !strings.Contains(result, "func main()") {
		t.Error("Code content should be preserved")
	}
	// Code blocks are indented with gray styling
	if !strings.Contains(result, gray) {
		t.Error("Code block should use gray styling")
	}
}

func TestRender_Headers(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"# Header 1", magenta},
		{"## Header 2", blue},
		{"### Header 3", cyan},
	}

	for _, tt := range tests {
		result := Render(tt.input)
		if !strings.Contains(result, bold) {
			t.Errorf("Render(%q) should be bold", tt.input)
		}
		if !strings.Contains(result, tt.want) {
			t.Errorf("Render(%q) should contain %q color", tt.input, tt.want)
		}
	}
}

func TestRender_Lists(t *testing.T) {
	tests := []struct {
		input      string
		wantBullet bool
	}{
		{"- item one", true},
		{"* item two", true},
		{"1. numbered", true},
		{"regular text", false},
	}

	for _, tt := range tests {
		result := Render(tt.input)
		hasBullet := strings.Contains(result, "•") || strings.Contains(result, "1.")
		if hasBullet != tt.wantBullet {
			t.Errorf("Render(%q) bullet=%v, want %v, got: %q", tt.input, hasBullet, tt.wantBullet, result)
		}
	}
}

func TestRender_Blockquote(t *testing.T) {
	input := "> This is a quote"
	result := Render(input)

	if !strings.Contains(result, "│") {
		t.Error("Blockquote should have vertical bar")
	}
	if !strings.Contains(result, italic) {
		t.Error("Blockquote should be italic")
	}
}

func TestRender_Links(t *testing.T) {
	input := "Check [this link](https://example.com)"
	result := Render(input)

	if !strings.Contains(result, underline) {
		t.Error("Link text should be underlined")
	}
	if !strings.Contains(result, "this link") {
		t.Error("Link text should be preserved")
	}
	if !strings.Contains(result, "example.com") {
		t.Error("URL should be shown")
	}
}

func TestRender_Mixed(t *testing.T) {
	input := "This has **bold** and `code` together"
	result := Render(input)

	if !strings.Contains(result, bold) {
		t.Error("Should contain bold")
	}
	if !strings.Contains(result, cyan) {
		t.Error("Should contain code styling")
	}
}

func TestRender_PreservesText(t *testing.T) {
	input := "Plain text without any markdown"
	result := Render(input)

	// Should contain the text
	if !strings.Contains(result, "Plain text without any markdown") {
		t.Errorf("Text should be preserved, got: %q", result)
	}
}
