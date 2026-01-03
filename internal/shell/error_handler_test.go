package shell

import (
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
