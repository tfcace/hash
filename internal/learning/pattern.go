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
	errPattern := extractErrorType(stderr)

	return Pattern{
		CommandPattern: cmdPattern,
		ErrorPattern:   errPattern,
		ExitCode:       exitCode,
	}
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

// extractErrorType extracts the core error type from stderr.
func extractErrorType(stderr string) string {
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

	// Default: first line, normalized
	lines := strings.Split(stderr, "\n")
	if len(lines) > 0 {
		return NormalizeError(lines[0])
	}

	return "unknown error"
}
