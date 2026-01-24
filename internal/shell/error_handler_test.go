package shell

import (
	"bytes"
	"strings"
	"testing"
)

func TestErrorHandler_FormatPrompt(t *testing.T) {
	handler := NewErrorHandler(nil)

	prompt := handler.FormatErrorPrompt(126, "Permission denied")

	if prompt == "" {
		t.Error("Prompt should not be empty")
	}
}

func TestErrorHandler_NoSuggestionForSuccess(t *testing.T) {
	handler := NewErrorHandler(nil)

	// Should not suggest anything for successful commands
	suggestion := handler.GetSuggestion("ls", "", 0)
	if suggestion != "" {
		t.Errorf("Expected no suggestion for exit code 0, got %q", suggestion)
	}
}

func TestErrorHandler_ShowsStderrInline(t *testing.T) {
	var buf bytes.Buffer
	h := NewErrorHandler(nil)
	h.out = &buf

	stderr := "error: cannot find module \"foo\"\nnote: run `go mod tidy` to fix"
	h.HandleError("go build", stderr, 1)

	output := buf.String()

	// Should show stderr lines
	if !strings.Contains(output, "cannot find module") {
		t.Error("Should show stderr content")
	}
	if !strings.Contains(output, "go mod tidy") {
		t.Error("Should show stderr hint")
	}
}
