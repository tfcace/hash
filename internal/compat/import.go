// internal/compat/import.go
package compat

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tfcace/hash/internal/trace"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// FilterWithCompat pre-processes a shell config file for compatibility.
// It returns the filtered content (with zsh-specific commands commented out)
// and a report of what was processed. The caller should execute the content
// through their own executor/interpreter.
func FilterWithCompat(path, shell string) (string, *Report, error) {
	return FilterWithDialect(path, shell, "bash")
}

// FilterWithDialect pre-processes a shell config file for the target parser dialect.
func FilterWithDialect(path, shell, targetDialect string) (string, *Report, error) {
	trace.Emit("compat", "filter_start", trace.LevelVerbose, map[string]any{
		"path":           path,
		"shell":          shell,
		"target_dialect": targetDialect,
	})

	info, err := os.Stat(path)
	if err != nil {
		return "", nil, err
	}

	report := NewReport(path, shell)
	report.SourceMtime = info.ModTime()

	content, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}

	// Pre-process: identify and filter shell-specific commands for the target dialect.
	lines := strings.Split(string(content), "\n")
	var filteredLines []string
	noops := NoopBuiltins()

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip empty lines and comments
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			filteredLines = append(filteredLines, line)
			continue
		}

		// Check for shell-specific tool initializations that Hash handles natively.
		if reason := shouldSkipLine(trimmed, targetDialect); reason != "" {
			report.AddSkipped(i+1, trimmed, reason)
			filteredLines = append(filteredLines, "# [hash-compat] "+line)
			continue
		}

		// Check for no-op builtins
		cmd := firstWord(trimmed)
		if fn, ok := noops[cmd]; ok {
			// Execute no-op and log
			args := strings.Fields(trimmed)[1:]
			fn(args, report) //nolint:errcheck // no-op functions don't fail
			// Replace with comment to preserve line numbers
			filteredLines = append(filteredLines, "# [hash-compat] "+line)
			// Update line number in last skipped item
			if len(report.SkippedItems) > 0 {
				report.SkippedItems[len(report.SkippedItems)-1].Line = i + 1
			}
			continue
		}

		// Track aliases and exports for reporting
		if strings.HasPrefix(trimmed, "alias ") {
			parts := strings.SplitN(trimmed[6:], "=", 2)
			if len(parts) == 2 {
				report.AddImported(ItemAlias, strings.TrimSpace(parts[0]), strings.Trim(parts[1], "'\""))
			}
		} else if strings.HasPrefix(trimmed, "export ") {
			parts := strings.SplitN(trimmed[7:], "=", 2)
			if len(parts) >= 1 {
				name := strings.TrimSpace(parts[0])
				value := ""
				if len(parts) == 2 {
					value = parts[1]
				}
				report.AddImported(ItemExport, name, value)
			}
		}

		filteredLines = append(filteredLines, line)
	}

	trace.Emit("compat", "filter_done", trace.LevelVerbose, map[string]any{
		"path":           path,
		"target_dialect": targetDialect,
		"skipped":        report.Summary.Skipped,
	})

	return strings.Join(filteredLines, "\n"), report, nil
}

// SourceWithCompat sources a shell rc file with graceful error handling.
// It skips zsh-specific builtins and recovers from parse errors.
// Note: This creates its own interpreter runner, so aliases/functions won't
// persist to the caller's shell. Use FilterWithCompat + executor for that.
//
//nolint:gocyclo // shell compatibility requires handling multiple file formats
func SourceWithCompat(ctx context.Context, path, shell string, stdout io.Writer) (*Report, error) {
	return SourceWithDialect(ctx, path, shell, "bash", stdout)
}

// SourceWithDialect sources a shell rc file with graceful error handling for the target dialect.
//
//nolint:gocyclo // shell compatibility requires handling multiple file formats
func SourceWithDialect(ctx context.Context, path, shell, targetDialect string, stdout io.Writer) (*Report, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	report := NewReport(path, shell)
	report.SourceMtime = info.ModTime()

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Pre-process: identify and filter shell-specific commands for the target dialect.
	lines := strings.Split(string(content), "\n")
	var filteredLines []string
	noops := NoopBuiltins()

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip empty lines and comments
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			filteredLines = append(filteredLines, line)
			continue
		}

		// Check for shell-specific tool initializations that Hash handles natively.
		if reason := shouldSkipLine(trimmed, targetDialect); reason != "" {
			report.AddSkipped(i+1, trimmed, reason)
			filteredLines = append(filteredLines, "# [hash-compat] "+line)
			continue
		}

		// Check for no-op builtins
		cmd := firstWord(trimmed)
		if fn, ok := noops[cmd]; ok {
			// Execute no-op and log
			args := strings.Fields(trimmed)[1:]
			fn(args, report) //nolint:errcheck // no-op functions don't fail
			// Replace with comment to preserve line numbers
			filteredLines = append(filteredLines, "# [hash-compat] "+line)
			// Update line number in last skipped item
			if len(report.SkippedItems) > 0 {
				report.SkippedItems[len(report.SkippedItems)-1].Line = i + 1
			}
			continue
		}

		// Track aliases and exports for reporting
		if strings.HasPrefix(trimmed, "alias ") {
			parts := strings.SplitN(trimmed[6:], "=", 2)
			if len(parts) == 2 {
				report.AddImported(ItemAlias, strings.TrimSpace(parts[0]), strings.Trim(parts[1], "'\""))
			}
		} else if strings.HasPrefix(trimmed, "export ") {
			parts := strings.SplitN(trimmed[7:], "=", 2)
			if len(parts) >= 1 {
				name := strings.TrimSpace(parts[0])
				value := ""
				if len(parts) == 2 {
					value = parts[1]
				}
				report.AddImported(ItemExport, name, value)
			}
		}

		filteredLines = append(filteredLines, line)
	}

	// Parse with error recovery and the target dialect.
	filtered := strings.Join(filteredLines, "\n")
	parser := syntax.NewParser(
		syntax.Variant(langVariantForDialect(targetDialect)),
		syntax.RecoverErrors(100),
	)
	prog, err := parser.Parse(strings.NewReader(filtered), path)
	if err != nil {
		// Log parse errors but continue
		report.AddSkipped(0, "", "parse error: "+err.Error())
	}

	if prog == nil {
		return report, nil
	}

	// Create interpreter and run
	if stdout == nil {
		stdout = io.Discard
	}

	runner, err := interp.New(
		interp.StdIO(nil, stdout, stdout),
		interp.Env(expand.ListEnviron(os.Environ()...)),
	)
	if err != nil {
		return report, err
	}

	// Run with error recovery - don't fail on individual command errors
	_ = runner.Run(ctx, prog)

	return report, nil
}

// firstWord returns the first word of a line.
func firstWord(line string) string {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func langVariantForDialect(dialect string) syntax.LangVariant {
	switch strings.ToLower(strings.TrimSpace(dialect)) {
	case "zsh":
		return syntax.LangZsh
	default:
		return syntax.LangBash
	}
}

// shouldSkipLine checks if a line should be skipped due to shell-specific syntax.
// Returns the reason to skip, or empty string if the line should be processed.
func shouldSkipLine(line, targetDialect string) string {
	// Starship init - Hash has built-in Starship support
	if strings.Contains(line, "starship init") {
		return "starship init (use prompt.mode = \"starship\" in Hash config)"
	}

	if strings.EqualFold(strings.TrimSpace(targetDialect), "zsh") {
		return ""
	}

	// Other common shell-specific inits that need native handling
	if strings.Contains(line, "zoxide init zsh") {
		return "zoxide init zsh (use 'eval \"$(zoxide init bash)\"' instead)"
	}
	if strings.Contains(line, "fzf --zsh") {
		return "fzf zsh integration (use 'eval \"$(fzf --bash)\"' instead)"
	}
	if strings.Contains(line, "direnv hook zsh") {
		return "direnv hook zsh (use 'eval \"$(direnv hook bash)\"' instead)"
	}

	// Zsh-specific plugins
	if strings.Contains(line, "zsh-autosuggestions") {
		return "zsh-autosuggestions (zsh-specific plugin)"
	}
	if strings.Contains(line, "zsh-syntax-highlighting") {
		return "zsh-syntax-highlighting (zsh-specific plugin)"
	}
	if strings.Contains(line, "zsh-completions") {
		return "zsh-completions (zsh-specific plugin)"
	}

	// Bun completions use zsh-specific syntax
	if strings.Contains(line, ".bun/_bun") || strings.Contains(line, "bun completions") {
		return "bun zsh completions (zsh-specific)"
	}

	return ""
}

// CheckSourceChanged checks if the source file has changed since last import.
func CheckSourceChanged(statePath string) (bool, error) {
	state, err := LoadState(statePath)
	if err != nil {
		return false, err
	}

	info, err := os.Stat(state.SourceFile)
	if err != nil {
		return false, err
	}

	return info.ModTime().After(state.SourceMtime), nil
}

// FormatChangeNotice returns a one-line notice about source file changes.
func FormatChangeNotice(rcFile string, skipped int) string {
	return fmt.Sprintf("hash: %s changed — %d zsh-specific items skipped (hash migrate status)\n",
		rcFile, skipped)
}
