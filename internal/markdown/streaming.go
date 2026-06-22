package markdown

import (
	"regexp"
	"strings"
)

// StreamingRenderer processes markdown text chunk-by-chunk and emits
// ANSI-styled output as patterns complete.
type StreamingRenderer struct {
	inCodeBlock   bool
	codeBlockLang string
	lineBuffer    strings.Builder
	inBold        bool
	inCode        bool
	pending       string // partial marker (e.g., single "*" or "`")
	midLine       bool   // a partial fragment of the current line was already emitted
}

// NewStreamingRenderer creates a new streaming markdown renderer.
func NewStreamingRenderer() *StreamingRenderer {
	return &StreamingRenderer{}
}

// Write processes a chunk of text and returns rendered output.
// Call Flush() after all chunks to get any remaining buffered content.
func (r *StreamingRenderer) Write(chunk string) string {
	var output strings.Builder

	for _, char := range chunk {
		r.lineBuffer.WriteRune(char)

		if char == '\n' {
			// Complete line - process it
			line := r.lineBuffer.String()
			r.lineBuffer.Reset()
			output.WriteString(r.processLine(line))
		}
	}

	return output.String()
}

// Flush returns any remaining buffered content with appropriate styling.
// The line is incomplete: its continuation arrives in later Write calls,
// so line-start decoration and inline state stay open for it.
func (r *StreamingRenderer) Flush() string {
	if r.lineBuffer.Len() == 0 {
		return ""
	}

	line := r.lineBuffer.String()
	r.lineBuffer.Reset()

	out := r.processLine(line)
	r.midLine = true
	return out
}

// Finish flushes any remaining content and closes styling left open by a
// line that never completed. Call once at end of stream instead of Flush.
func (r *StreamingRenderer) Finish() string {
	out := r.Flush()
	if r.pending == "*" {
		out += "*"
		r.pending = ""
	}
	if r.inCode || r.inBold || (r.midLine && r.inCodeBlock) {
		out += reset
	}
	r.inCode = false
	r.inBold = false
	r.midLine = false
	return out
}

// Reset clears all state for reuse.
func (r *StreamingRenderer) Reset() {
	r.inCodeBlock = false
	r.codeBlockLang = ""
	r.lineBuffer.Reset()
	r.inBold = false
	r.inCode = false
	r.pending = ""
	r.midLine = false
}

// processLine handles a line or, after a Flush, a fragment of one.
// Continuation fragments skip line-start decoration (indent, headers,
// lists, fences): the first fragment already decided how the line opens.
func (r *StreamingRenderer) processLine(line string) string {
	// Preserve the newline if present
	hasNewline := strings.HasSuffix(line, "\n")
	content := strings.TrimSuffix(line, "\n")
	continuation := r.midLine
	if hasNewline {
		r.midLine = false
	}

	var result string
	fenceMarker := false

	// 1. Code block markers
	if !continuation && strings.HasPrefix(content, "```") {
		result = r.handleCodeBlockMarker(content)
		fenceMarker = true
	} else if r.inCodeBlock {
		// 2. Inside code block - just indent, no dim (dim causes visual artifacts)
		// Open the color and indent once per line; close at the real line end.
		result = content
		if !continuation {
			result = gray + "  " + result
		}
		if hasNewline {
			result += reset
		}
	} else if header := r.tryHeaderAt(content, continuation); header != "" {
		// 3. Headers
		result = header
	} else if list := r.tryListAt(content, continuation, hasNewline); list != "" {
		// 4. Lists
		result = list
	} else {
		// 5. Regular line - inline processing
		result = r.processInline(content, hasNewline)
	}

	// A fence marker line is invisible: it must not leave behind its own
	// newline, or code blocks gain a blank line above and below.
	if hasNewline && !fenceMarker {
		return result + "\n"
	}
	return result
}

// tryHeaderAt suppresses header detection on continuation fragments.
func (r *StreamingRenderer) tryHeaderAt(content string, continuation bool) string {
	if continuation {
		return ""
	}
	return r.tryHeader(content)
}

// tryListAt suppresses list detection on continuation fragments.
func (r *StreamingRenderer) tryListAt(content string, continuation, complete bool) string {
	if continuation {
		return ""
	}
	return r.tryList(content, complete)
}

// handleCodeBlockMarker toggles code block state and returns styled output.
func (r *StreamingRenderer) handleCodeBlockMarker(line string) string {
	if !r.inCodeBlock {
		// Opening marker — absorb silently (no header line)
		r.inCodeBlock = true
		r.codeBlockLang = strings.TrimPrefix(line, "```")
		r.codeBlockLang = strings.TrimSpace(r.codeBlockLang)
		return ""
	}
	// Closing marker
	r.inCodeBlock = false
	r.codeBlockLang = ""
	return "" // No output for closing ```
}

// tryHeader checks if line is a header and returns styled output, or empty string.
func (r *StreamingRenderer) tryHeader(line string) string {
	if strings.HasPrefix(line, "#### ") {
		return bold + strings.TrimPrefix(line, "#### ") + reset
	}
	if strings.HasPrefix(line, "### ") {
		return bold + cyan + strings.TrimPrefix(line, "### ") + reset
	}
	if strings.HasPrefix(line, "## ") {
		return bold + blue + strings.TrimPrefix(line, "## ") + reset
	}
	if strings.HasPrefix(line, "# ") {
		return bold + magenta + strings.TrimPrefix(line, "# ") + reset
	}
	return ""
}

// listPrefixRegex matches ordered list items like "1. ", "12. ", etc.
var listPrefixRegex = regexp.MustCompile(`^(\s*)(\d+)\. (.*)$`)

// tryList checks if line is a list item and returns styled output, or empty string.
func (r *StreamingRenderer) tryList(line string, complete bool) string {
	// Check leading whitespace for indentation
	trimmed := strings.TrimLeft(line, " \t")
	indent := line[:len(line)-len(trimmed)]

	// Unordered lists
	if strings.HasPrefix(trimmed, "- ") {
		content := r.processInline(trimmed[2:], complete)
		return indent + cyan + "•" + reset + " " + content
	}
	if strings.HasPrefix(trimmed, "* ") {
		content := r.processInline(trimmed[2:], complete)
		return indent + cyan + "•" + reset + " " + content
	}

	// Ordered lists
	if matches := listPrefixRegex.FindStringSubmatch(line); matches != nil {
		indent := matches[1]
		num := matches[2]
		text := matches[3]
		content := r.processInline(text, complete)
		return indent + cyan + num + "." + reset + " " + content
	}

	return ""
}

// processInline handles inline patterns: **bold** and `code`.
// When complete is false the text is a mid-line fragment: pending markers
// and open styling carry over to the fragment that continues the line.
func (r *StreamingRenderer) processInline(text string, complete bool) string {
	var result strings.Builder
	runes := []rune(text)
	i := 0

	for i < len(runes) {
		char := runes[i]

		// Handle backtick for inline code
		if char == '`' {
			if r.inCode {
				// End inline code
				result.WriteString(reset)
				r.inCode = false
			} else {
				// Start inline code
				result.WriteString(cyan)
				r.inCode = true
			}
			i++
			continue
		}

		// Handle asterisk for bold
		if char == '*' {
			if r.pending == "*" {
				// Second asterisk - toggle bold
				r.pending = ""
				if r.inBold {
					result.WriteString(reset)
					r.inBold = false
				} else {
					result.WriteString(bold)
					r.inBold = true
				}
				i++
				continue
			}
			// First asterisk - mark as pending
			r.pending = "*"
			i++
			continue
		}

		// If we have a pending asterisk and this isn't another asterisk,
		// output it as literal (single * in MVP is literal)
		if r.pending == "*" {
			result.WriteRune('*')
			r.pending = ""
		}

		result.WriteRune(char)
		i++
	}

	// Mid-line fragment: leave pending markers and styling open for the
	// continuation.
	if !complete {
		return result.String()
	}

	// Flush any remaining pending marker at end of line
	if r.pending == "*" {
		result.WriteRune('*')
		r.pending = ""
	}

	// Close any open formatting at end of line
	// (prevents formatting from bleeding across lines unexpectedly)
	if r.inCode {
		result.WriteString(reset)
		r.inCode = false
	}
	if r.inBold {
		result.WriteString(reset)
		r.inBold = false
	}

	return result.String()
}
