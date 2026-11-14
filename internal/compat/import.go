// internal/compat/import.go
package compat

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/tfcace/hash/internal/executor"
	"github.com/tfcace/hash/internal/trace"
	"mvdan.cc/sh/v3/syntax"
)

// FilterWithDialect pre-processes a shell config file for the target parser
// dialect. A file the filter fails on (a bug in it) is an error, so that
// startup reports the file as not loaded and goes on; the caller names the
// file.
func FilterWithDialect(path, shell, targetDialect string) (filtered string, report *Report, err error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	return filterFile(path, string(content), shell, targetDialect)
}

// FilterSourced returns content, the file at path that a source or . reads
// at runtime in a file written for from ("": hash's own, or none), as the
// dialect runs it. A startup file of from's shell (the .bashrc a migrated
// .bash_profile sources) is filtered as that shell's, as the file sourcing
// it was, and a zsh file (ShellForPath) in the bash dialect as zsh's; any
// other file is as it is. It is the shell's source filter
// (executor.SetSourceFilter). A file that does not load all says so
// (notLoaded, nil when it does): a statement the filter stops at, or the
// filter's failure, which loads none of it.
func FilterSourced(path string, content []byte, dialect, from string) (filtered []byte, notLoaded *NotLoaded) {
	shell := ShellForPath(path)
	switch {
	case from != "" && startupFileShell(path) == from:
	case shell != "zsh", langVariantForDialect(dialect) == syntax.LangZsh:
		return content, nil
	}
	text, report, err := filterFile(path, string(content), shell, dialect)
	var failed filterFailure
	switch {
	case errors.As(err, &failed):
		return nil, &NotLoaded{File: path, Load: executor.LoadedNone, Reason: failed.reason()}
	case len(report.NotLoaded) > 0:
		return []byte(text), &report.NotLoaded[0]
	}
	return []byte(text), nil
}

// filterFailure is the filter failing on a file (a bug in it): the file
// does not load.
type filterFailure struct{ cause any }

func (f filterFailure) Error() string { return "not loaded, " + f.reason() }

// reason says why the file did not load.
func (f filterFailure) reason() string {
	return fmt.Sprintf("the compatibility filter failed: %v", f.cause)
}

// filterFile filters content, the file at path written for shell, for the
// target dialect.
func filterFile(path, content, shell, targetDialect string) (filtered string, report *Report, err error) {
	defer func() {
		if r := recover(); r != nil {
			filtered, report, err = "", nil, filterFailure{r}
		}
	}()
	trace.Emit("compat", "filter_start", trace.LevelVerbose, map[string]any{
		"path":           path,
		"shell":          shell,
		"target_dialect": targetDialect,
	})

	filtered, report = filterContent(content, shell, targetDialect, func() *Report {
		return NewReport(path, shell)
	})
	for i := range report.SkippedItems {
		report.SkippedItems[i].File = path
	}
	for i := range report.NotLoaded {
		report.NotLoaded[i].File = path
	}

	trace.Emit("compat", "filter_done", trace.LevelVerbose, map[string]any{
		"path":           path,
		"target_dialect": targetDialect,
		"skipped":        report.Summary.Skipped,
	})

	return filtered, report, nil
}

// filterContent is filterRC; a test makes it fail.
var filterContent = filterRC

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

	// `${(%):...}` uses Zsh parameter-expansion flags. It appears in
	// Powerlevel10k's instant-prompt setup and cannot be parsed as Bash.
	if strings.Contains(line, "${(%):") {
		return "zsh parameter expansion flags"
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
