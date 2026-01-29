package completion

import (
	"context"
	"os"
	"strings"
)

// EnvProvider is the interface for getting environment variables from the executor.
type EnvProvider interface {
	Environ() []string
}

// EnvCompleter completes environment variable names.
type EnvCompleter struct {
	provider EnvProvider
	envFunc  func() []string // For testing; overrides provider if set
}

// NewEnvCompleter creates a new environment variable completer.
// If provider is nil, falls back to os.Environ().
func NewEnvCompleter(provider EnvProvider) *EnvCompleter {
	return &EnvCompleter{
		provider: provider,
	}
}

// Name returns the completer name.
func (c *EnvCompleter) Name() string {
	return "env"
}

// Complete returns completions for environment variables.
// Triggers when the user types $ or ${ followed by variable name prefix.
func (c *EnvCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	if pos > len(line) {
		pos = len(line)
	}

	// Find $VAR or ${VAR pattern before cursor
	prefix, hasDollar := extractEnvPrefix(line, pos)
	if !hasDollar {
		return Result{}, nil
	}

	// Get environment variables from provider or fallback
	var environ []string
	if c.envFunc != nil {
		environ = c.envFunc()
	} else if c.provider != nil {
		environ = c.provider.Environ()
	} else {
		environ = os.Environ()
	}

	var items []Item
	for _, env := range environ {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		name, value := parts[0], parts[1]

		if strings.HasPrefix(name, prefix) {
			items = append(items, Item{
				Value:       "$" + name, // Include $ so replacement works correctly
				Display:     name,
				Description: truncateValue(value, 40),
				Icon:        "$",
			})
		}
	}

	return Result{Items: items}, nil
}

// extractEnvPrefix extracts the environment variable prefix being typed.
// Returns the prefix (without $) and whether we're in an env var context.
func extractEnvPrefix(line string, pos int) (string, bool) {
	if pos == 0 {
		return "", false
	}

	// Work backwards from cursor to find $ or ${
	end := pos
	start := pos

	// Find end of variable name (stop at non-identifier chars)
	for start > 0 {
		ch := line[start-1]
		if ch == '$' {
			// Found the dollar sign
			return line[start:end], true
		}
		if ch == '{' && start > 1 && line[start-2] == '$' {
			// Found ${
			return line[start:end], true
		}
		// Valid env var chars: A-Z, a-z, 0-9, _
		if !isEnvChar(ch) {
			break
		}
		start--
	}

	return "", false
}

// isEnvChar returns true if the byte is valid in an environment variable name.
func isEnvChar(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_'
}

// truncateValue truncates a value to maxLen characters with ellipsis.
func truncateValue(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
