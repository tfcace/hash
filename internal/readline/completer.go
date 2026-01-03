package readline

import (
	"context"
	"strings"

	"github.com/chzyer/readline"
	"github.com/tfcace/hash/internal/completion"
)

// CompleterAdapter adapts our completion system to chzyer/readline.
type CompleterAdapter struct {
	router *completion.Router
}

// NewCompleterAdapter creates a new completer adapter.
func NewCompleterAdapter(router *completion.Router) *CompleterAdapter {
	return &CompleterAdapter{router: router}
}

// Do implements readline.AutoCompleter.
func (c *CompleterAdapter) Do(line []rune, pos int) ([][]rune, int) {
	if c.router == nil {
		return nil, 0
	}

	ctx := context.Background()
	result, err := c.router.Complete(ctx, string(line), pos)
	if err != nil || len(result.Items) == 0 {
		return nil, 0
	}

	// Extract the word being completed to calculate replacement length
	word := extractWordBeforePos(line, pos)

	// Convert to readline format
	// chzyer/readline expects SUFFIXES (the part to append), not full words
	// The second return value (length) tells readline how many chars of input matched
	candidates := make([][]rune, len(result.Items))
	wordStr := string(word)
	for i, item := range result.Items {
		fullValue := result.Prefix + item.Value
		// Strip the matched prefix to get just the suffix
		suffix := fullValue
		if len(wordStr) > 0 && len(fullValue) >= len(wordStr) &&
			strings.EqualFold(fullValue[:len(wordStr)], wordStr) {
			suffix = fullValue[len(wordStr):]
		}
		candidates[i] = []rune(suffix)
	}

	// Return the length of the matched prefix
	return candidates, len(word)
}

// extractWordBeforePos extracts the word being completed.
func extractWordBeforePos(line []rune, pos int) []rune {
	if pos > len(line) {
		pos = len(line)
	}

	// Find start of word (go backwards until space or start)
	start := pos
	for start > 0 && line[start-1] != ' ' && line[start-1] != '\t' {
		start--
	}

	return line[start:pos]
}

// Compile-time check that CompleterAdapter implements readline.AutoCompleter
var _ readline.AutoCompleter = (*CompleterAdapter)(nil)
