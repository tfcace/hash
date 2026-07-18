package shell

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tfcace/hash/internal/learning"
)

// ErrorHandler renders error and learned-fix banners.
type ErrorHandler struct {
	out io.Writer
}

// NewErrorHandler creates a new error handler writing to stderr.
func NewErrorHandler() *ErrorHandler {
	return &ErrorHandler{}
}

// HandleCommandNotFound displays a command-not-found error with suggestions.
func (h *ErrorHandler) HandleCommandNotFound(cmd string, suggestions []string, installHint string) {
	out := h.out
	if out == nil {
		out = os.Stderr
	}

	// Header
	fmt.Fprintf(out, "\n\033[31m✗ %s: command not found\033[0m\n", cmd)

	// Suggestions
	if len(suggestions) > 0 {
		fmt.Fprintf(out, "  \033[90m│\033[0m did you mean: %s?\n", strings.Join(suggestions, ", "))
	}

	// Install hint
	if installHint != "" {
		fmt.Fprintf(out, "  \033[90m│\033[0m install: \033[33m%s\033[0m\n", installHint)
	}

	// Footer
	fmt.Fprintf(out, "  \033[90m└─ ?? to explain\033[0m\n")
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
	fmt.Fprintf(out, "  \033[90m└─ → to accept at prompt · esc to dismiss · ?? to explain\033[0m\n")
}
