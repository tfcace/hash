package learning

import (
	"regexp"
	"strings"
)

// Pattern represents a normalized error pattern.
type Pattern struct {
	CommandPattern string // Normalized command pattern
	ErrorPattern   string // Normalized error pattern
	ExitCode       int    // Exit code that produced this error
}

var (
	// Patterns for normalization
	quotedFileRe = regexp.MustCompile(`['"]([^'"]+)['"]`)
	pathRe       = regexp.MustCompile(`(/[a-zA-Z0-9_./-]+)`)
	lineNumRe    = regexp.MustCompile(`\bline\s+(\d+)`)
	atLineRe     = regexp.MustCompile(`\bat\s+line\s+(\d+)`)
)

// normalizeErrorFor is NormalizeError keeping quoted words the user typed
// in command, which identify the mistake rather than vary with it.
func normalizeErrorFor(line, command string) string {
	if strings.Contains(line, "\x00") {
		return NormalizeError(line) // the placeholder below would collide
	}
	typed := map[string]bool{}
	for _, f := range strings.Fields(command) {
		typed[strings.ToLower(strings.Trim(f, `'"`))] = true
	}
	var kept []string
	masked := quotedFileRe.ReplaceAllStringFunc(strings.ToLower(line), func(q string) string {
		if word := q[1 : len(q)-1]; typed[word] {
			kept = append(kept, q)
			return "\x00"
		}
		return q
	})
	normalized := NormalizeError(masked)
	for _, q := range kept {
		normalized = strings.Replace(normalized, "\x00", q, 1)
	}
	return normalized
}

// NormalizeError converts a specific error message into a pattern.
func NormalizeError(err string) string {
	normalized := strings.ToLower(err)

	// Replace quoted filenames
	normalized = quotedFileRe.ReplaceAllString(normalized, "'{file}'")

	// Replace paths (but not after replacing quoted files)
	if !strings.Contains(normalized, "'{file}'") {
		normalized = pathRe.ReplaceAllString(normalized, "{path}")
	}

	// Replace line numbers
	normalized = lineNumRe.ReplaceAllString(normalized, "line {n}")
	normalized = atLineRe.ReplaceAllString(normalized, "at line {n}")

	return normalized
}

// ExtractPattern creates a Pattern from command, error, and exit code.
func ExtractPattern(command, stderr string, exitCode int) Pattern {
	// Normalize command
	cmdPattern := normalizeCommand(command)

	// Normalize error
	errPattern := extractErrorType(stderr, command)

	return Pattern{
		CommandPattern: cmdPattern,
		ErrorPattern:   errPattern,
		ExitCode:       exitCode,
	}
}

// Related reports whether fix plausibly addresses the failed command rather
// than being whatever the user ran next: it runs the same program (npm
// install after npm test) or shares an argument (chmod +x deploy.sh after
// ./deploy.sh). A different tool that names nothing from the failure (claude
// after a jj error) is the user moving on.
func Related(failed, fix string) bool {
	if normalizeCommand(failed) == normalizeCommand(fix) {
		return true
	}
	failedArgs := argWords(failed)
	for word := range argWords(fix) {
		if failedArgs[word] {
			return true
		}
	}
	return false
}

// argWords returns a command's non-flag arguments, plus the program itself
// when it is a script path (./deploy.sh is what chmod +x deploy.sh names).
func argWords(cmd string) map[string]bool {
	words := map[string]bool{}
	for i, f := range strings.Fields(cmd) {
		f = strings.TrimPrefix(strings.Trim(f, `'"`), "./")
		if f == "" || strings.HasPrefix(f, "-") {
			continue
		}
		if i == 0 && !strings.Contains(f, "/") && !strings.Contains(f, ".") {
			continue
		}
		words[f] = true
	}
	return words
}

// normalizeCommand extracts a pattern from a command.
func normalizeCommand(cmd string) string {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return ""
	}

	// Check if it's a script execution
	first := parts[0]
	if strings.HasPrefix(first, "./") || strings.HasPrefix(first, "/") {
		if strings.HasSuffix(first, ".sh") || strings.HasSuffix(first, ".py") || strings.HasSuffix(first, ".rb") {
			return "{script}"
		}
		return "{path}"
	}

	return first
}

// extractErrorType extracts the core error type from stderr. A word the
// error quotes from the command itself stays in the pattern: "unrecognized
// subcommand 'stauts'" is about that typo, and its fix is not another's.
func extractErrorType(stderr, command string) string {
	lower := strings.ToLower(stderr)

	// Common error patterns
	patterns := []struct {
		contains string
		pattern  string
	}{
		{"permission denied", "permission denied"},
		{"command not found", "command not found"},
		{"no such file or directory", "file not found"},
		{"syntax error", "syntax error"},
		{"connection refused", "connection refused"},
		{"timeout", "timeout"},
		{"cannot find", "not found"},
		{"not recognized", "command not found"},
		{"does not exist", "not found"},
		{"already exists", "already exists"},
		{"is a directory", "is a directory"},
		{"is not a directory", "is not a directory"},
	}

	for _, p := range patterns {
		if strings.Contains(lower, p.contains) {
			return p.pattern
		}
	}

	// Default: first non-empty line, normalized
	lines := strings.Split(stderr, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			return normalizeErrorFor(line, command)
		}
	}

	return "unknown error"
}
