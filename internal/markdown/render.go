package markdown

import (
	"regexp"
	"strings"
)

// ANSI escape codes
const (
	reset     = "\033[0m"
	bold      = "\033[1m"
	dim       = "\033[2m"
	italic    = "\033[3m"
	underline = "\033[4m"

	// Colors
	red     = "\033[31m"
	green   = "\033[32m"
	yellow  = "\033[33m"
	blue    = "\033[34m"
	magenta = "\033[35m"
	cyan    = "\033[36m"
	gray    = "\033[90m"
)

// Patterns for inline markdown
var (
	// Bold: **text** or __text__
	boldPattern = regexp.MustCompile(`\*\*(.+?)\*\*|__(.+?)__`)
	// Inline code: `code`
	codePattern = regexp.MustCompile("`([^`\n]+)`")
	// Strikethrough: ~~text~~
	strikePattern = regexp.MustCompile(`~~(.+?)~~`)
	// Links: [text](url)
	linkPattern = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

// Render converts markdown text to ANSI-styled terminal output.
func Render(text string) string {
	lines := strings.Split(text, "\n")
	var result []string
	inCodeBlock := false

	for _, line := range lines {
		// Handle code blocks
		if strings.HasPrefix(line, "```") {
			if !inCodeBlock {
				inCodeBlock = true
				continue // the language tag is not displayed
			} else {
				inCodeBlock = false
				continue
			}
		}

		if inCodeBlock {
			// Code block content - gray with indent
			result = append(result, gray+"  "+line+reset)
			continue
		}

		// Process regular lines
		result = append(result, reopenStyles(processLine(line)))
	}

	return strings.Join(result, "\n")
}

// sgrPattern matches one SGR escape sequence.
var sgrPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// reopenStyles makes nested spans compose. Every span closes with a full
// reset, which also ends any span around it (bold around inline code), so
// after each reset the styles still open are emitted again. Adjacent opening
// sequences belong to one span (bold + color for a header). A span left open
// at the end of the line is closed there.
func reopenStyles(line string) string {
	var out strings.Builder
	var open []string // opening sequences of the spans still open, outermost first
	last, prevEnd := 0, -1
	for _, m := range sgrPattern.FindAllStringIndex(line, -1) {
		out.WriteString(line[last:m[1]])
		seq := line[m[0]:m[1]]
		last = m[1]
		switch {
		case seq == reset:
			if len(open) > 0 {
				open = open[:len(open)-1]
			}
			out.WriteString(strings.Join(open, ""))
			prevEnd = -1 // what follows a reset opens a new span
			continue
		case m[0] == prevEnd && len(open) > 0:
			open[len(open)-1] += seq
		default:
			open = append(open, seq)
		}
		prevEnd = m[1]
	}
	out.WriteString(line[last:])
	if len(open) > 0 {
		out.WriteString(reset)
	}
	return out.String()
}

func processLine(line string) string {
	// Headers
	if strings.HasPrefix(line, "# ") {
		return bold + magenta + strings.TrimPrefix(line, "# ") + reset
	}
	if strings.HasPrefix(line, "## ") {
		return bold + blue + strings.TrimPrefix(line, "## ") + reset
	}
	if strings.HasPrefix(line, "### ") {
		return bold + cyan + strings.TrimPrefix(line, "### ") + reset
	}
	if strings.HasPrefix(line, "#### ") {
		return bold + strings.TrimPrefix(line, "#### ") + reset
	}

	// Horizontal rule
	if line == "---" || line == "***" || line == "___" {
		return gray + "────────────────────────────────" + reset
	}

	// Unordered lists
	trimmed := strings.TrimLeft(line, " \t")
	indent := strings.Repeat(" ", len(line)-len(trimmed))
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
		content := renderInline(trimmed[2:])
		return indent + cyan + "•" + reset + " " + content
	}

	// Ordered lists
	if matched, _ := regexp.MatchString(`^\d+\. `, trimmed); matched {
		parts := strings.SplitN(trimmed, ". ", 2)
		if len(parts) == 2 {
			content := renderInline(parts[1])
			return indent + cyan + parts[0] + "." + reset + " " + content
		}
	}

	// Blockquotes
	if strings.HasPrefix(line, "> ") {
		content := renderInline(strings.TrimPrefix(line, "> "))
		return gray + "│ " + italic + content + reset
	}

	return renderInline(line)
}

func renderInline(text string) string {
	result := text

	// Process links first: [text](url) -> text (underlined) with url in gray.
	// Later passes insert escape sequences, whose "[" the link pattern would
	// otherwise take for a link.
	result = linkPattern.ReplaceAllStringFunc(result, func(match string) string {
		// Extract text and url
		parts := linkPattern.FindStringSubmatch(match)
		if len(parts) == 3 {
			return underline + blue + parts[1] + reset + gray + " (" + parts[2] + ")" + reset
		}
		return match
	})

	// Process bold (before italic to avoid conflicts)
	result = boldPattern.ReplaceAllStringFunc(result, func(match string) string {
		// Extract the content between ** or __
		inner := strings.TrimPrefix(match, "**")
		inner = strings.TrimSuffix(inner, "**")
		inner = strings.TrimPrefix(inner, "__")
		inner = strings.TrimSuffix(inner, "__")
		return bold + inner + reset
	})

	// Process inline code (before italic to preserve backticks)
	result = codePattern.ReplaceAllStringFunc(result, func(match string) string {
		inner := strings.Trim(match, "`")
		return cyan + inner + reset
	})

	// Process strikethrough
	result = strikePattern.ReplaceAllStringFunc(result, func(match string) string {
		inner := strings.Trim(match, "~")
		// Use dim for strikethrough effect (no true strikethrough in most terminals)
		return dim + gray + inner + reset
	})

	// Process italic last (single * or _)
	// This is trickier - we need to avoid matching inside bold or words
	result = processItalic(result)

	return result
}

func processItalic(text string) string {
	// Simple approach: match *word* patterns that aren't part of **
	var result strings.Builder
	i := 0
	runes := []rune(text)

	for i < len(runes) {
		// Skip if this is a ** (bold marker)
		if i+1 < len(runes) && runes[i] == '*' && runes[i+1] == '*' {
			result.WriteRune(runes[i])
			i++
			continue
		}

		// Check for *italic* pattern
		if runes[i] == '*' {
			// Find closing *
			end := -1
			for j := i + 1; j < len(runes); j++ {
				if runes[j] == '*' {
					// Make sure it's not **
					if j+1 < len(runes) && runes[j+1] == '*' {
						continue
					}
					end = j
					break
				}
				if runes[j] == '\n' {
					break
				}
			}
			if end > i+1 {
				result.WriteString(italic)
				result.WriteString(string(runes[i+1 : end]))
				result.WriteString(reset)
				i = end + 1
				continue
			}
		}

		result.WriteRune(runes[i])
		i++
	}

	return result.String()
}
