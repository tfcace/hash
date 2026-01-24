package shell

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tfcace/hash/internal/learning"
)

// ErrorHandler manages error detection and fix suggestions.
type ErrorHandler struct {
	fixStore *learning.FixStore
	out      io.Writer
}

// NewErrorHandler creates a new error handler.
func NewErrorHandler(store *learning.FixStore) *ErrorHandler {
	return &ErrorHandler{fixStore: store}
}

// HandleError processes a command error and suggests fixes.
func (h *ErrorHandler) HandleError(command string, stderr string, exitCode int) {
	if exitCode == 0 {
		return
	}

	// Extract pattern
	pattern := learning.ExtractPattern(command, stderr, exitCode)

	// Check for learned fix
	if h.fixStore != nil {
		fix, found := h.fixStore.GetFix(pattern)
		if found {
			if fix.Score >= 0.7 {
				h.showLearnedFix(fix, true) // high confidence
				return
			} else if fix.Score >= 0.5 {
				h.showLearnedFix(fix, false) // low confidence
				return
			}
		}
	}

	// Show generic error prompt
	h.showErrorPrompt(exitCode, stderr)
}

func (h *ErrorHandler) showLearnedFix(fix learning.Fix, highConfidence bool) {
	out := h.out
	if out == nil {
		out = os.Stderr
	}

	if highConfidence {
		// High confidence: → prefix
		fmt.Fprintf(out, "\n\033[33m✗ Learned fix available\033[0m\n")
		fmt.Fprintf(out, "\n\033[32m→\033[0m %s    \033[90m(worked %d×)\033[0m\n",
			fix.Fix, fix.SuccessCount)
	} else {
		// Low confidence: ? prefix
		fmt.Fprintf(out, "\n\033[33m✗ Possible fix\033[0m\n")
		fmt.Fprintf(out, "\n\033[33m?\033[0m %s    \033[90m(tried %d×, worked %d×)\033[0m\n",
			fix.Fix, fix.SuccessCount+fix.FailureCount, fix.SuccessCount)
	}
	fmt.Fprintf(out, "  \033[90m[Enter: run] [Tab: edit] [?: ask agent] [Esc: dismiss]\033[0m\n")
}

func (h *ErrorHandler) showErrorPrompt(exitCode int, stderr string) {
	out := h.out
	if out == nil {
		out = os.Stderr
	}

	// Header
	fmt.Fprintf(out, "\n\033[31m✗ Exit %d\033[0m\n", exitCode)

	// Show last 2-3 lines of stderr
	if stderr != "" {
		lines := strings.Split(strings.TrimSpace(stderr), "\n")
		start := 0
		if len(lines) > 3 {
			start = len(lines) - 3
		}
		for _, line := range lines[start:] {
			if line != "" {
				fmt.Fprintf(out, "  \033[90m│\033[0m %s\n", line)
			}
		}
	}

	// Footer
	fmt.Fprintf(out, "  \033[90m└─ ?? to explain\033[0m\n")
}

// FormatErrorPrompt formats an error prompt string.
func (h *ErrorHandler) FormatErrorPrompt(exitCode int, stderr string) string {
	return fmt.Sprintf("x Exit %d | ?? to explain", exitCode)
}

// GetSuggestion returns a fix suggestion for the given error, or empty string.
func (h *ErrorHandler) GetSuggestion(command string, stderr string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}

	if h.fixStore == nil {
		return ""
	}

	pattern := learning.ExtractPattern(command, stderr, exitCode)
	fix, found := h.fixStore.GetFix(pattern)
	if found && fix.Score >= 0.7 {
		return fix.Fix
	}

	return ""
}

// RecordFixAttempt records whether a suggested fix worked.
func (h *ErrorHandler) RecordFixAttempt(command string, stderr string, exitCode int, fix string, success bool) {
	if h.fixStore == nil {
		return
	}

	pattern := learning.ExtractPattern(command, stderr, exitCode)
	h.fixStore.RecordFix(pattern, fix, success)
}
