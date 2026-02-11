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
// Completes only in command position (first word or after a pipe),
// mirroring executable completion so path arguments (e.g. `cd ...`) still
// fall through to filesystem completion.
func (c *AliasCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	// Extract pipe context to handle commands after pipes.
	pipeLine, pipePos := ExtractPipeContext(line, pos)

	// Only complete if we're in command position (first word).
	parts := strings.Fields(pipeLine[:pipePos])
	isCommandPosition := len(parts) == 0 || (len(parts) == 1 && !strings.HasSuffix(pipeLine[:pipePos], " "))
	if !isCommandPosition {
		return Result{}, nil
	}

	prefix := ""
	if len(parts) == 1 {
		prefix = parts[0]
	}

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
