package shell

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/learning"
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

func TestErrorHandler_ShowsLowConfidenceFixes(t *testing.T) {
	// Create a mock store with a low-confidence fix
	tmpDir := t.TempDir()
	store, err := learning.NewFixStore(filepath.Join(tmpDir, "learning.db"))
	if err != nil {
		t.Fatalf("NewFixStore error: %v", err)
	}
	defer store.Close()

	// Record a fix with low confidence (2 tries, 1 success = 0.5)
	pattern := learning.Pattern{
		CommandPattern: "{script}",
		ErrorPattern:   "permission denied",
		ExitCode:       126,
	}
	store.RecordFix(pattern, "chmod +x {script}", true)
	store.RecordFix(pattern, "chmod +x {script}", false)

	var buf bytes.Buffer
	h := NewErrorHandler(store)
	h.out = &buf

	h.HandleError("./script.sh", "permission denied", 126)

	output := buf.String()

	// Should show with "?" prefix for low confidence
	if !strings.Contains(output, "?") {
		t.Error("Should show ? prefix for low confidence fix")
	}
	if !strings.Contains(output, "chmod") {
		t.Error("Should show the fix command")
	}
	if !strings.Contains(output, "tried") {
		t.Error("Should show attempt count for low confidence")
	}
}

func TestErrorHandler_HandleCommandNotFound(t *testing.T) {
	var buf bytes.Buffer
	h := NewErrorHandler(nil)
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
	h := NewErrorHandler(nil)
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
