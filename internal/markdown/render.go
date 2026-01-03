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
	// Italic: *text* or _text_ (but not inside words for underscores)
	italicPattern = regexp.MustCompile(`(?:^|[^*])\*([^*\n]+?)\*(?:[^*]|$)|(?:^|\s)_([^_\n]+?)_(?:\s|$)`)
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
	codeBlockLang := ""

	for _, line := range lines {
		// Handle code blocks
		if strings.HasPrefix(line, "```") {
			if !inCodeBlock {
				inCodeBlock = true
				codeBlockLang = strings.TrimPrefix(line, "```")
				if codeBlockLang != "" {
					result = append(result, dim+gray+"─── "+codeBlockLang+" ───"+reset)
				}
				continue
			} else {
				inCodeBlock = false
				codeBlockLang = ""
				continue
			}
		}

		if inCodeBlock {
			// Code block content - dim cyan
			result = append(result, dim+cyan+"  "+line+reset)
			continue
		}

		// Process regular lines
		processed := processLine(line)
		result = append(result, processed)
	}

	return strings.Join(result, "\n")
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

	// Process bold first (before italic to avoid conflicts)
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
		return dim + cyan + inner + reset
	})

	// Process strikethrough
	result = strikePattern.ReplaceAllStringFunc(result, func(match string) string {
		inner := strings.Trim(match, "~")
		// Use dim for strikethrough effect (no true strikethrough in most terminals)
		return dim + gray + inner + reset
	})

	// Process links: [text](url) -> text (underlined) with url in gray
	result = linkPattern.ReplaceAllStringFunc(result, func(match string) string {
		// Extract text and url
		parts := linkPattern.FindStringSubmatch(match)
		if len(parts) == 3 {
			return underline + blue + parts[1] + reset + gray + " (" + parts[2] + ")" + reset
		}
		return match
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
