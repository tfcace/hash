package shell

import (
	"fmt"
	"os"
	"strings"

	"github.com/tfcace/hash/internal/learning"
)

// ErrorHandler manages error detection and fix suggestions.
type ErrorHandler struct {
	fixStore *learning.FixStore
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
		if found && fix.Score >= 0.7 {
			h.showLearnedFix(fix)
			return
		}
	}

	// Show generic error prompt
	h.showErrorPrompt(exitCode, stderr)
}

func (h *ErrorHandler) showLearnedFix(fix learning.Fix) {
	fmt.Fprintf(os.Stderr, "\n\033[33mx Learned fix available\033[0m\n")
	fmt.Fprintf(os.Stderr, "\n\033[32m->\033[0m %s    \033[90m(worked %d times)\033[0m\n", fix.Fix, fix.SuccessCount)
	fmt.Fprintf(os.Stderr, "  \033[90m[Enter: run] [Tab: edit] [?: ask agent] [Esc: dismiss]\033[0m\n")
}

func (h *ErrorHandler) showErrorPrompt(exitCode int, stderr string) {
	// Truncate stderr if too long
	stderrLines := strings.Split(stderr, "\n")
	if len(stderrLines) > 3 {
		stderrLines = stderrLines[:3]
	}

	fmt.Fprintf(os.Stderr, "\n\033[31mx Exit %d\033[0m | \033[90m?? to explain\033[0m\n", exitCode)
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
