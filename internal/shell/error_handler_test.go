package shell

import (
	"bytes"
	"strings"
	"testing"
)

func TestErrorHandler_HandleCommandNotFound(t *testing.T) {
	var buf bytes.Buffer
	h := NewErrorHandler()
	h.out = &buf

	h.HandleCommandNotFound("jq", []string{"jp", "jj"}, "brew install jq")

	output := buf.String()

	// Should show command name
	if !strings.Contains(output, "jq") {
		t.Error("Should show command name")
	}
	// Should show "command not found"
	if !strings.Contains(output, "command not found") {
		t.Error("Should show 'command not found' message")
	}
	// Should show suggestions
	if !strings.Contains(output, "did you mean") {
		t.Error("Should show 'did you mean' prompt")
	}
	if !strings.Contains(output, "jp") {
		t.Error("Should show first suggestion")
	}
	// Should show install hint
	if !strings.Contains(output, "install") {
		t.Error("Should show install label")
	}
	if !strings.Contains(output, "brew install jq") {
		t.Error("Should show install command")
	}
	// Should show agent help hint
	if !strings.Contains(output, "??") {
		t.Error("Should show ?? hint")
	}
}

func TestErrorHandler_HandleCommandNotFound_NoSuggestions(t *testing.T) {
	var buf bytes.Buffer
	h := NewErrorHandler()
	h.out = &buf

	h.HandleCommandNotFound("xyz123", nil, "")

	output := buf.String()

	// Should show command name and error
	if !strings.Contains(output, "xyz123") {
		t.Error("Should show command name")
	}
	if !strings.Contains(output, "command not found") {
		t.Error("Should show 'command not found' message")
	}
	// Should NOT show suggestions
	if strings.Contains(output, "did you mean") {
		t.Error("Should not show 'did you mean' with no suggestions")
	}
	// Should NOT show install hint
	if strings.Contains(output, "install:") {
		t.Error("Should not show install hint when empty")
	}
}
