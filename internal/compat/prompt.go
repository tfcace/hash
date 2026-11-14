// internal/compat/prompt.go
package compat

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/tfcace/hash/internal/tilde"
)

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
)

// FormatWelcomePromptFiles returns the welcome message showing all detected config files.
func FormatWelcomePromptFiles(files ShellFiles) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("Welcome to Hash!"))
	b.WriteString("\n\n")

	fmt.Fprintf(&b, "Detected %s config files:\n", files.Shell)
	for _, f := range files.Files() {
		fmt.Fprintf(&b, "  %s\n", lipgloss.NewStyle().Bold(true).Render(tilde.Abbrev(f)))
	}
	b.WriteString("\nWould you like to load compatible settings?\n\n")

	b.WriteString("  [Y] Yes, load settings\n")
	b.WriteString("  [n] No, start fresh\n")
	b.WriteString("  [?] What does this do?\n\n")

	other := "bash"
	if files.Shell == "bash" {
		other = "zsh"
	}
	b.WriteString(dimStyle.Render(
		"(To load from a different shell later: hash migrate --from " + other + ")"))

	return b.String()
}

// FormatImportSummary returns a formatted summary of the migration: what
// loaded, or, when it loads later (not into the shell that imports it),
// what will load.
func FormatImportSummary(report *Report, later bool) string {
	var b strings.Builder

	names := make([]string, len(report.Files))
	for i, f := range report.Files {
		names[i] = tilde.Abbrev(f)
	}
	heading := "Loaded from"
	if later {
		heading = "Will load from"
	}
	fmt.Fprintf(&b, "%s %s:\n", heading, strings.Join(names, " + "))

	if report.Summary.Aliases > 0 {
		b.WriteString("  ")
		b.WriteString(successStyle.Render("✓ " + Count(report.Summary.Aliases, "alias", "aliases")))
		b.WriteString("\n")
	}
	if report.Summary.Exports > 0 {
		b.WriteString("  ")
		b.WriteString(successStyle.Render("✓ " + Count(report.Summary.Exports, "environment variable", "environment variables")))
		b.WriteString("\n")
	}
	if report.Summary.Functions > 0 {
		b.WriteString("  ")
		b.WriteString(successStyle.Render("✓ " + Count(report.Summary.Functions, "function", "functions")))
		b.WriteString("\n")
	}

	if report.Summary.Skipped > 0 {
		b.WriteString("\n")
		b.WriteString(warnStyle.Render("Skipped " + Count(report.Summary.Skipped, "item", "items") + ":"))
		b.WriteString("\n")

		// Show first 3 skipped items
		shown := 0
		for _, item := range report.SkippedItems {
			if shown >= 3 {
				remaining := len(report.SkippedItems) - shown
				b.WriteString("  ")
				b.WriteString(dimStyle.Render(fmt.Sprintf("... (%d more, run 'hash migrate status' for full list)", remaining)))
				b.WriteString("\n")
				break
			}
			b.WriteString("  ")
			b.WriteString(dimStyle.Render(fmt.Sprintf("Line %d: %s", item.Line, truncate(item.Content, 50))))
			b.WriteString("\n")
			shown++
		}
	}
	for _, n := range report.NotLoaded {
		b.WriteString("\n")
		b.WriteString(warnStyle.Render(n.String()))
		b.WriteString("\n")
	}

	return b.String()
}

// The generated header of ~/.hashrc starts and ends with these lines; the
// user's own lines follow it.
const (
	hashrcHeaderStart = "# Hash migration: shell config loaded from:\n"
	hashrcHeaderEnd   = "# Add your own customizations below:\n\n"
)

// FormatHashrcCommentFiles returns the generated header for ~/.hashrc. It
// holds no source commands: hash sources the migrated files itself at
// startup with the configured parser dialect.
func FormatHashrcCommentFiles(files []string) string {
	var b strings.Builder

	b.WriteString(hashrcHeaderStart)
	for _, file := range files {
		fmt.Fprintf(&b, "#   %s\n", tilde.Abbrev(file))
	}
	b.WriteString("#\n")
	b.WriteString("# Files are sourced at startup with the configured shell dialect.\n")
	b.WriteString("# Run 'hash migrate status' to see what was skipped\n")
	b.WriteString("#\n")
	b.WriteString(hashrcHeaderEnd)

	return b.String()
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
