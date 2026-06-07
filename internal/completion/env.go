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
	provider      EnvProvider
	envFunc       func() []string // For testing; overrides provider if set
	maskSensitive bool            // Mask values of sensitive env vars
}

// NewEnvCompleter creates a new environment variable completer.
// If provider is nil, falls back to os.Environ().
func NewEnvCompleter(provider EnvProvider) *EnvCompleter {
	return &EnvCompleter{
		provider:      provider,
		maskSensitive: true, // Enabled by default
	}
}

// SetMaskSensitive enables or disables masking of sensitive env var values.
func (c *EnvCompleter) SetMaskSensitive(enabled bool) {
	c.maskSensitive = enabled
}

// Name returns the completer name.
func (c *EnvCompleter) Name() string {
	return "env"
}

// Complete returns completions for environment variables.
// Triggers when the user types $ or ${ followed by variable name prefix.
func (c *EnvCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	pos = clampCursor(line, pos)

	// Find $VAR or ${VAR pattern before cursor
	prefix, hasDollar := extractEnvPrefix(line, pos)
	if !hasDollar {
		return Result{}, nil
	}

	// Get environment variables from provider or fallback
	var environ []string
	switch {
	case c.envFunc != nil:
		environ = c.envFunc()
	case c.provider != nil:
		environ = c.provider.Environ()
	default:
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
			displayValue := value
			if c.maskSensitive && isSensitiveEnvName(name) {
				displayValue = maskValue(value)
			}
			items = append(items, Item{
				Value:       "$" + name, // Include $ so replacement works correctly
				Display:     name,
				Description: truncateValue(displayValue, 40),
				Icon:        "$",
			})
			if len(items) >= completionItemLimit {
				break
			}
		}
	}

	return Result{Items: items}, nil
}

// sensitivePatterns contains substrings that indicate a sensitive env var.
var sensitivePatterns = []string{
	"KEY",
	"SECRET",
	"TOKEN",
	"PASSWORD",
	"PASSWD",
	"CREDENTIAL",
	"PRIVATE",
	"API_KEY",
	"APIKEY",
	"AUTH",
}

// isSensitiveEnvName returns true if the env var name suggests it contains sensitive data.
func isSensitiveEnvName(name string) bool {
	upper := strings.ToUpper(name)
	for _, pattern := range sensitivePatterns {
		if strings.Contains(upper, pattern) {
			return true
		}
	}
	return false
}

// maskValue masks a sensitive value, showing only the first 4 characters.
// Returns the original value if it's too short to mask meaningfully.
func maskValue(value string) string {
	const visibleChars = 4
	const maskChar = "•"

	if len(value) <= visibleChars {
		// Too short to mask meaningfully
		return strings.Repeat(maskChar, len(value))
	}

	// Show first 4 chars, mask the rest (up to 8 bullets to avoid long strings)
	maskedLen := len(value) - visibleChars
	if maskedLen > 8 {
		maskedLen = 8
	}
	return value[:visibleChars] + strings.Repeat(maskChar, maskedLen)
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
