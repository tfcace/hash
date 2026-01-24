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
