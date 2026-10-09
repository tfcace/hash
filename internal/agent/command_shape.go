package agent

import (
	"strings"
	"unicode"
)

// commandLeadInWords are the words a one-line lead-in must contain for the
// line below it to be read as a command. They keep an output listing such as
// "Found these files:" followed by a path from turning into a run button.
var commandLeadInWords = map[string]bool{
	"command": true, "commands": true, "run": true, "try": true,
	"use": true, "execute": true, "one-liner": true,
}

// commandFromResponse reports whether an agent reply is a runnable command and
// returns that command. A bare single-line command qualifies, as before. So
// does a one-line lead-in such as "The command is:" followed by exactly one
// command line, which may be indented, wrapped in inline code, fenced, or
// prefixed with a "$ " prompt marker. Anything longer stays output.
func commandFromResponse(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	if looksLikeCommand(text) {
		return text, true
	}

	lines := responseLines(text)
	var candidate string
	switch len(lines) {
	case 1:
		candidate = lines[0]
	case 2:
		if !isCommandLeadIn(lines[0]) {
			return "", false
		}
		candidate = lines[1]
	default:
		return "", false
	}

	cmd := bareCommandLine(candidate)
	if looksLikeCommand(cmd) {
		return cmd, true
	}
	return "", false
}

// responseLines splits a reply into trimmed, non-empty lines, dropping code
// fence markers.
func responseLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "```") {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

// isCommandLeadIn reports whether a line introduces a command: it ends with a
// colon and contains one of commandLeadInWords as a whole word.
func isCommandLeadIn(line string) bool {
	line = strings.TrimSpace(unwrapMarkdownInline(line))
	if !strings.HasSuffix(line, ":") {
		return false
	}
	words := strings.FieldsFunc(strings.ToLower(line), func(r rune) bool {
		return !unicode.IsLetter(r) && r != '-'
	})
	for _, w := range words {
		if commandLeadInWords[w] {
			return true
		}
	}
	return false
}

// bareCommandLine strips the decoration an agent may put around a command
// line: inline code and a leading "$ " prompt marker.
func bareCommandLine(line string) string {
	line = strings.TrimSpace(unwrapMarkdownInline(line))
	return strings.TrimSpace(strings.TrimPrefix(line, "$ "))
}
