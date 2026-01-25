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
// Call this after all chunks have been written.
func (r *StreamingRenderer) Flush() string {
	if r.lineBuffer.Len() == 0 {
		return ""
	}

	line := r.lineBuffer.String()
	r.lineBuffer.Reset()

	// Process the incomplete line (no trailing newline)
	return r.processLine(line)
}

// Reset clears all state for reuse.
func (r *StreamingRenderer) Reset() {
	r.inCodeBlock = false
	r.codeBlockLang = ""
	r.lineBuffer.Reset()
	r.inBold = false
	r.inCode = false
	r.pending = ""
}

// processLine handles a complete line (may or may not have trailing newline).
func (r *StreamingRenderer) processLine(line string) string {
	// Preserve the newline if present
	hasNewline := strings.HasSuffix(line, "\n")
	content := strings.TrimSuffix(line, "\n")

	var result string

	// 1. Code block markers
	if strings.HasPrefix(content, "```") {
		result = r.handleCodeBlockMarker(content)
	} else if r.inCodeBlock {
		// 2. Inside code block - no parsing, just style
		result = dim + cyan + "  " + content + reset
	} else if header := r.tryHeader(content); header != "" {
		// 3. Headers
		result = header
	} else if list := r.tryList(content); list != "" {
		// 4. Lists
		result = list
	} else {
		// 5. Regular line - inline processing
		result = r.processInline(content)
	}

	if hasNewline {
		return result + "\n"
	}
	return result
}

// handleCodeBlockMarker toggles code block state and returns styled output.
func (r *StreamingRenderer) handleCodeBlockMarker(line string) string {
	if !r.inCodeBlock {
		// Opening marker
		r.inCodeBlock = true
		r.codeBlockLang = strings.TrimPrefix(line, "```")
		r.codeBlockLang = strings.TrimSpace(r.codeBlockLang)
		if r.codeBlockLang != "" {
			return dim + gray + "─── " + r.codeBlockLang + " ───" + reset
		}
		return "" // No output for bare ```
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
func (r *StreamingRenderer) tryList(line string) string {
	// Check leading whitespace for indentation
	trimmed := strings.TrimLeft(line, " \t")
	indent := line[:len(line)-len(trimmed)]

	// Unordered lists
	if strings.HasPrefix(trimmed, "- ") {
		content := r.processInline(trimmed[2:])
		return indent + cyan + "•" + reset + " " + content
	}
	if strings.HasPrefix(trimmed, "* ") {
		content := r.processInline(trimmed[2:])
		return indent + cyan + "•" + reset + " " + content
	}

	// Ordered lists
	if matches := listPrefixRegex.FindStringSubmatch(line); matches != nil {
		indent := matches[1]
		num := matches[2]
		text := matches[3]
		content := r.processInline(text)
		return indent + cyan + num + "." + reset + " " + content
	}

	return ""
}

// processInline handles inline patterns: **bold** and `code`.
func (r *StreamingRenderer) processInline(text string) string {
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
				result.WriteString(dim + cyan)
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
