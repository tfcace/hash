package shell

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/tfcace/hash/internal/executor"
)

// terminalSeq matches OSC sequences (BEL- or ST-terminated), CSI sequences,
// and two-byte escapes.
var terminalSeq = regexp.MustCompile(`\x1b(?:\][^\x07\x1b]*(?:\x07|\x1b\\)|\[[0-9:;<=>?]*[\x20-\x2f]*[\x40-\x7e]|[\x40-\x5a\x5c-\x5f])`)

// ptyStderrFallback derives error text for a failed PTY command. A PTY merges
// the child's stderr into its output stream, so the direct stderr capture
// stays empty; the sanitized tail of the captured merged output is the best
// available record of what went wrong.
func ptyStderrFallback(result *executor.Result) string {
	if result == nil || !result.UsedPTY || result.ExitCode == 0 {
		return ""
	}

	text := result.StderrTail
	if text == "" {
		text = result.CapturedOutput
	}
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n") // carriage-return progress lines
	text = stripTerminalSequences(text)
	text = strings.TrimRight(text, "\n\t ")

	// Keep the tail: errors print last.
	if len(text) > maxStderrCapture {
		text = text[len(text)-maxStderrCapture:]
		for text != "" && !utf8.RuneStart(text[0]) {
			text = text[1:]
		}
	}
	return text
}

// stripTerminalSequences removes ANSI escape sequences and remaining control
// characters (except newline and tab) from terminal output.
func stripTerminalSequences(s string) string {
	s = terminalSeq.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}
