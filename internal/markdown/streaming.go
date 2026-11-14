package markdown

import (
	"regexp"
	"strings"
)

// codeLinePrefix opens every rendered code block line. The wrapper knows a
// code line by it and leaves the line unwrapped.
const codeLinePrefix = gray + "  "

// StreamingRenderer processes markdown text chunk-by-chunk and emits
// ANSI-styled output as patterns complete.
//
// Inline state: inline says where the text is (plain, after a lone "*"
// that may still become "**", or in a code span, where everything but the
// closing backtick is literal); inBold may enclose a code span but never
// opens or closes inside one.
//
// Line state: phase says whether the current line's start (indent, header,
// list marker, fence) has been rendered. lineBuffer holds the bytes of the
// line not rendered yet; at lineOpening that may be a start still too short
// to tell what it opens (held by Flush), at lineOpen a continuation.
//
// Table state: table holds the lines of a table still arriving (or one
// line that may turn out to head a table), drawn when a line that is not
// the table's arrives or the stream ends.
type StreamingRenderer struct {
	// Width is the columns a table may take; below 1 is no limit.
	Width int

	table     []string
	tableOpen bool // the last held line had no newline

	inCodeBlock bool
	lineBuffer  strings.Builder
	phase       linePhase
	inBold      bool
	inline      inlineState
}

// linePhase is how far the current line has been rendered.
type linePhase int

const (
	// lineOpening: nothing of the line is rendered yet; its first fragment
	// decides how it opens.
	lineOpening linePhase = iota
	// lineOpen: a fragment was flushed, so the line's start is decided and
	// what follows renders as its continuation, up to the newline.
	lineOpen
)

// inlineState is where processInline is within a line's inline markup.
type inlineState int

const (
	inlinePlain inlineState = iota
	inlineStar              // a lone "*": literal unless the next rune is "*" too
	inlineCode              // inside a `code span`
)

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
// so line-start decoration and inline state stay open for it. A line whose
// start is still ambiguous (a fence, header or list marker not yet complete)
// stays buffered until more of it arrives.
func (r *StreamingRenderer) Flush() string {
	if r.lineBuffer.Len() == 0 || r.lineStartUndecided(r.lineBuffer.String()) {
		return ""
	}
	return r.flushPartial()
}

// flushPartial renders the buffered fragment and marks the line as started.
func (r *StreamingRenderer) flushPartial() string {
	line := r.lineBuffer.String()
	r.lineBuffer.Reset()

	out := r.processLine(line)
	r.phase = lineOpen
	return out
}

// orderedPrefixRe matches the start of an ordered list marker before its
// space has arrived ("1", "12.").
var orderedPrefixRe = regexp.MustCompile(`^\d+\.?$`)

// lineStartUndecided reports whether a line's opening fragment could still
// turn out to be a fence, header or list item. processLine decides how a
// line opens from its first fragment only, so such a fragment must wait.
func (r *StreamingRenderer) lineStartUndecided(fragment string) bool {
	if r.phase == lineOpen {
		return false
	}
	// A fence line renders nothing and its language follows the backticks:
	// hold it whole, and hold the backticks that may still become one.
	if strings.HasPrefix(fragment, "`") {
		onlyBackticks := strings.Trim(fragment, "`") == ""
		return (onlyBackticks && len(fragment) <= 3) || strings.HasPrefix(fragment, "```")
	}
	if r.inCodeBlock {
		return false
	}
	// Headers are short: render them whole so the style spans the line.
	if strings.HasPrefix(fragment, "#") {
		return true
	}
	trimmed := strings.TrimLeft(fragment, " \t")
	// A table line is drawn when its table ends.
	if strings.HasPrefix(trimmed, "|") {
		return true
	}
	return trimmed == "" || trimmed == "-" || trimmed == "*" || orderedPrefixRe.MatchString(trimmed)
}

// Finish flushes any remaining content and closes styling left open by a
// line that never completed. Call once at end of stream instead of Flush.
func (r *StreamingRenderer) Finish() string {
	var out string
	if r.lineBuffer.Len() > 0 {
		out = r.flushPartial()
	}
	out += r.endTable()
	if r.inline == inlineStar {
		out += "*"
	}
	if r.inline == inlineCode || r.inBold || (r.phase == lineOpen && r.inCodeBlock) {
		out += reset
	}
	r.Reset() // the stream is over: the next one starts outside any block
	return out
}

// Reset clears all state for reuse.
func (r *StreamingRenderer) Reset() {
	*r = StreamingRenderer{Width: r.Width}
}

// processLine handles a line or, after a Flush, a fragment of one.
// Continuation fragments skip line-start decoration (indent, headers,
// lists, fences): the first fragment already decided how the line opens.
func (r *StreamingRenderer) processLine(line string) string {
	// Preserve the newline if present
	hasNewline := strings.HasSuffix(line, "\n")
	content := strings.TrimSuffix(line, "\n")
	continuation := r.phase == lineOpen
	if hasNewline {
		r.phase = lineOpening // the next line starts fresh
	}

	// A table's lines are held until a line that is not the table's.
	var before string
	if !continuation {
		if !r.inCodeBlock && isTableLine(content) {
			return r.holdTableLine(content, hasNewline)
		}
		before = r.endTable()
	}

	var result string
	fenceMarker := false

	// 1. Code block markers
	if !continuation && strings.HasPrefix(content, "```") {
		result = r.handleCodeBlockMarker()
		fenceMarker = true
	} else if r.inCodeBlock {
		// 2. Inside code block - just indent, no dim (dim causes visual artifacts)
		// Open the color and indent once per line; close at the real line end.
		result = content
		if !continuation {
			result = codeLinePrefix + result
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
		return before + result + "\n"
	}
	return before + result
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

// handleCodeBlockMarker toggles code block state. Fence lines (and the
// opening fence's language) render nothing.
func (r *StreamingRenderer) handleCodeBlockMarker() string {
	r.inCodeBlock = !r.inCodeBlock
	return ""
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

	for _, char := range text {
		r.inlineRune(&result, char)
	}

	// Mid-line fragment: leave pending markers and styling open for the
	// continuation.
	if !complete {
		return result.String()
	}

	// Flush any remaining pending marker at end of line
	if r.inline == inlineStar {
		result.WriteRune('*')
	}

	// Close any open formatting at end of line
	// (prevents formatting from bleeding across lines unexpectedly)
	if r.inline == inlineCode || r.inBold {
		result.WriteString(reset)
	}
	r.inline, r.inBold = inlinePlain, false

	return result.String()
}

// inlineRune feeds one rune of a line's text through the inline markup.
func (r *StreamingRenderer) inlineRune(out *strings.Builder, char rune) {
	switch r.inline {
	case inlineCode:
		// Code spans are literal up to the closing backtick.
		if char == '`' {
			r.inline = inlinePlain
			out.WriteString(reset + r.openStyles())
		} else {
			out.WriteRune(char)
		}
		return
	case inlineStar:
		r.inline = inlinePlain
		if char == '*' {
			r.toggleBold(out) // "**"
			return
		}
		out.WriteByte('*') // a lone asterisk is literal
	}

	switch char {
	case '*':
		r.inline = inlineStar
	case '`':
		r.inline = inlineCode
		out.WriteString(cyan)
	default:
		out.WriteRune(char)
	}
}

// toggleBold opens or closes bold. Closing resets every SGR attribute, so the
// styles still open are re-emitted after the reset.
func (r *StreamingRenderer) toggleBold(out *strings.Builder) {
	r.inBold = !r.inBold
	if r.inBold {
		out.WriteString(bold)
		return
	}
	out.WriteString(reset + r.openStyles())
}

// openStyles returns the escape codes for the inline styles still open.
func (r *StreamingRenderer) openStyles() string {
	var s string
	if r.inBold {
		s += bold
	}
	if r.inline == inlineCode {
		s += cyan
	}
	return s
}
