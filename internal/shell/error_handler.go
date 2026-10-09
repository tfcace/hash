package shell

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tfcace/hash/internal/learning"
)

// acceptFooter closes a banner whose suggestion is waiting as ghost text at
// the next prompt.
const acceptFooter = "  \033[90m└─ → to accept at prompt · esc to dismiss · ?? to explain\033[0m\n"

// ErrorHandler renders error and learned-fix banners.
type ErrorHandler struct {
	out io.Writer
}

// NewErrorHandler creates a new error handler writing to stderr.
func NewErrorHandler() *ErrorHandler {
	return &ErrorHandler{}
}

// HandleCommandNotFound displays a command-not-found error with suggestions.
// offerAccept says the first suggestion will be waiting as ghost text at the
// next prompt, so the footer may teach the accept key.
func (h *ErrorHandler) HandleCommandNotFound(cmd string, suggestions []string, installHint string, offerAccept bool) {
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
	if offerAccept {
		fmt.Fprint(out, acceptFooter)
		return
	}
	fmt.Fprintf(out, "  \033[90m└─ ?? to explain\033[0m\n")
}

// HandleNotExecutable displays a path that exists but cannot be run.
func (h *ErrorHandler) HandleNotExecutable(cmd, reason string) {
	out := h.out
	if out == nil {
		out = os.Stderr
	}

	fmt.Fprintf(out, "\n\033[31m✗ %s: %s\033[0m\n", cmd, reason)
	fmt.Fprintf(out, "  \033[90m└─ ?? to explain\033[0m\n")
}

// showDidYouMean displays a deterministic typo correction (e.g. a close
// branch name), using the same key hints as the learned-fix banner.
func (h *ErrorHandler) showDidYouMean(suggestion string) {
	out := h.out
	if out == nil {
		out = os.Stderr
	}

	fmt.Fprintf(out, "\n\033[33m✗ Did you mean\033[0m\n")
	fmt.Fprintf(out, "\n\033[32m→\033[0m %s\n", suggestion)
	fmt.Fprint(out, acceptFooter)
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
	fmt.Fprint(out, acceptFooter)
}
