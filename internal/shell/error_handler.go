package shell

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/tfcace/hash/internal/agentupdate"
	"github.com/tfcace/hash/internal/learning"
	"github.com/tfcace/hash/internal/prompt"
)

// acceptHints are the keys of a banner whose suggestion is waiting as ghost
// text at the next prompt.
var acceptHints = []string{"→ to accept at prompt", "esc to dismiss", "?? to explain"}

// ErrorHandler renders error and learned-fix banners.
type ErrorHandler struct {
	out     io.Writer
	palette func() prompt.Palette // the interface's colors; nil is the default palette
}

// NewErrorHandler creates a new error handler writing to stderr.
func NewErrorHandler() *ErrorHandler {
	return &ErrorHandler{}
}

func (h *ErrorHandler) writer() io.Writer {
	if h.out == nil {
		return os.Stderr
	}
	return h.out
}

func (h *ErrorHandler) colors() prompt.Palette {
	if h.palette == nil {
		return prompt.DefaultPalette()
	}
	return h.palette()
}

// arrow opens the row of a command the banner offers.
func (h *ErrorHandler) arrow() string {
	return fgCode(h.colors().Success) + "→" + ansiReset + " "
}

// HandleCommandNotFound opens a command-not-found banner: the name, close
// names when there are any, and how to install it when hash knows. The
// line's RefusalFooter closes it.
func (h *ErrorHandler) HandleCommandNotFound(cmd string, suggestions []string, installHint string) {
	out := h.writer()
	fmt.Fprintf(out, "\n%s\n", frameTop("✗ "+cmd, "command not found", fgCode(h.colors().Error)))
	if len(suggestions) > 0 {
		fmt.Fprintln(out, frameRow("did you mean: "+strings.Join(suggestions, ", ")+"?"))
	}
	if installHint != "" {
		fmt.Fprintln(out, frameRow("install: "+fgCode(h.colors().Warning)+installHint+ansiReset))
	}
}

// RefusalFooter closes the command-not-found banners of a line the user ran,
// once the line is over: it teaches the accept key when the next prompt
// offers the corrected line, else that ?? explains the failure. A refusal
// outside the user's line (a hook, a background job) gets no footer: there
// is nothing to accept, and ?? explains the user's line, not it.
func (h *ErrorHandler) RefusalFooter(offerAccept bool) {
	if offerAccept {
		fmt.Fprintln(h.writer(), frameFoot(acceptHints...))
		return
	}
	fmt.Fprintln(h.writer(), frameFoot("?? to explain"))
}

// HandleNotExecutable displays a path that exists but cannot be run.
func (h *ErrorHandler) HandleNotExecutable(cmd, reason string) {
	out := h.writer()
	fmt.Fprintf(out, "\n%s\n", frameTop("✗ "+cmd, reason, fgCode(h.colors().Error)))
	fmt.Fprintln(out, frameFoot("?? to explain"))
}

// showDidYouMean displays a deterministic typo correction (e.g. a close
// branch name), with the same keys as the learned-fix banner.
func (h *ErrorHandler) showDidYouMean(suggestion string) {
	out := h.writer()
	fmt.Fprintf(out, "\n%s\n", frameTop("did you mean", "", fgCode(h.colors().Warning)))
	fmt.Fprintln(out, frameRow(h.arrow()+suggestion))
	fmt.Fprintln(out, frameFoot(acceptHints...))
}

// showUpdateAvailable tells the user a newer adapter exists, in the shape of
// the other prompt banners: the versions, the offered command, and the keys.
func (h *ErrorHandler) showUpdateAvailable(n agentupdate.Notice, now time.Time) {
	out := h.writer()
	summary := fmt.Sprintf("%s %s → %s · installed %s", agentupdate.DisplayName, n.Installed, n.Latest, formatAge(n.InstalledAt, now))
	fmt.Fprintf(out, "\n%s\n", frameTop("update", summary, fgCode(h.colors().Primary)))
	fmt.Fprintln(out, frameRow(h.arrow()+updateCommand))
	fmt.Fprintln(out, frameFoot("→ to accept at prompt", "esc to dismiss"))
}

// showLearnedFix offers a fix that worked before. A high-confidence one is
// waiting at the next prompt; a low-confidence one is only shown.
func (h *ErrorHandler) showLearnedFix(fix learning.Fix, highConfidence bool) {
	out := h.writer()
	warning := fgCode(h.colors().Warning)
	if !highConfidence {
		tally := fmt.Sprintf("tried %d×, worked %d×", fix.SuccessCount+fix.FailureCount, fix.SuccessCount)
		fmt.Fprintf(out, "\n%s\n", frameTop("possible fix", tally, warning))
		fmt.Fprintln(out, frameRow(warning+"?"+ansiReset+" "+fix.Fix))
		// Not pre-typed at the prompt, so there is nothing to accept.
		fmt.Fprintln(out, frameFoot("?? to explain"))
		return
	}
	fmt.Fprintf(out, "\n%s\n", frameTop("fix", fmt.Sprintf("worked %d×", fix.SuccessCount), warning))
	fmt.Fprintln(out, frameRow(h.arrow()+fix.Fix))
	fmt.Fprintln(out, frameFoot(acceptHints...))
}
