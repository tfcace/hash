package completion

import (
	"context"
	"strings"
)

// FunctionProvider is the interface for getting function names from the executor.
type FunctionProvider interface {
	Functions() []string
}

// AliasCompleter completes user-defined aliases and functions.
type AliasCompleter struct {
	provider FunctionProvider
}

// NewAliasCompleter creates a new alias completer.
func NewAliasCompleter(provider FunctionProvider) *AliasCompleter {
	return &AliasCompleter{provider: provider}
}

// Name returns the completer name.
func (c *AliasCompleter) Name() string {
	return "alias"
}

// Complete returns completions for aliases and functions.
// Unlike ExecutableCompleter, this completes anywhere (not just command position)
// because functions can be arguments to xargs, find -exec, etc.
func (c *AliasCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	// Extract the word being completed
	prefix := extractCommandWord(line, pos)

	var items []Item
	for _, name := range c.provider.Functions() {
		if strings.HasPrefix(name, prefix) {
			items = append(items, Item{
				Value:   name,
				Display: name,
				Icon:    "ƒ",
			})
		}
	}

	return Result{Items: items}, nil
}

// extractCommandWord extracts the command word at the cursor position.
// Unlike extractWord in file.go, this also handles shell operators as word boundaries.
func extractCommandWord(line string, pos int) string {
	if pos > len(line) {
		pos = len(line)
	}

	// Find start of word (go backwards until whitespace, operator, or start)
	start := pos
	for start > 0 && !isCommandWordBreak(line[start-1]) {
		start--
	}

	return line[start:pos]
}

// isCommandWordBreak returns true if the byte is a command word boundary.
func isCommandWordBreak(b byte) bool {
	return b == ' ' || b == '\t' || b == '|' || b == '&' || b == ';' || b == '(' || b == ')'
}
