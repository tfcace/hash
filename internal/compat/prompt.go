// internal/compat/prompt.go
package compat

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
)

// FormatWelcomePrompt returns the welcome message for first-run migration.
func FormatWelcomePrompt(shell, rcFile string) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("Welcome to Hash!"))
	b.WriteString("\n\n")

	b.WriteString(fmt.Sprintf("Detected %s from your previous shell (%s).\n",
		lipgloss.NewStyle().Bold(true).Render(rcFile), shell))
	b.WriteString("Would you like to load compatible settings?\n\n")

	b.WriteString("  [Y] Yes, load settings\n")
	b.WriteString("  [n] No, start fresh\n")
	b.WriteString("  [?] What does this do?\n\n")

	b.WriteString(dimStyle.Render(
		"(To load from a different shell later: hash migrate --from bash)"))

	return b.String()
}

// FormatWelcomePromptFiles returns the welcome message showing all detected config files.
func FormatWelcomePromptFiles(files ShellFiles) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("Welcome to Hash!"))
	b.WriteString("\n\n")

	b.WriteString(fmt.Sprintf("Detected %s config files:\n", files.Shell))
	for _, f := range files.Files() {
		displayPath := f
		if home := os.Getenv("HOME"); home != "" && strings.HasPrefix(f, home) {
			displayPath = "~" + f[len(home):]
		}
		b.WriteString(fmt.Sprintf("  %s\n", lipgloss.NewStyle().Bold(true).Render(displayPath)))
	}
	b.WriteString("\nWould you like to load compatible settings?\n\n")

	b.WriteString("  [Y] Yes, load settings\n")
	b.WriteString("  [n] No, start fresh\n")
	b.WriteString("  [?] What does this do?\n\n")

	b.WriteString(dimStyle.Render(
		"(To load from a different shell later: hash migrate --from bash)"))

	return b.String()
}

// FormatImportSummary returns a formatted summary of the migration.
func FormatImportSummary(report *Report) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Loaded from %s:\n", report.SourceFile))

	if report.Summary.Aliases > 0 {
		b.WriteString("  ")
		b.WriteString(successStyle.Render(fmt.Sprintf("✓ %d aliases", report.Summary.Aliases)))
		b.WriteString("\n")
	}
	if report.Summary.Exports > 0 {
		b.WriteString("  ")
		b.WriteString(successStyle.Render(fmt.Sprintf("✓ %d environment variables", report.Summary.Exports)))
		b.WriteString("\n")
	}
	if report.Summary.Functions > 0 {
		b.WriteString("  ")
		b.WriteString(successStyle.Render(fmt.Sprintf("✓ %d functions", report.Summary.Functions)))
		b.WriteString("\n")
	}

	if report.Summary.Skipped > 0 {
		b.WriteString("\n")
		noun := "items"
		if report.Summary.Skipped == 1 {
			noun = "item"
		}
		b.WriteString(warnStyle.Render(fmt.Sprintf("Skipped %d %s:", report.Summary.Skipped, noun)))
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

	return b.String()
}

// FormatHashrcComment returns the comment block for .hashrc.
func FormatHashrcComment(rcFile string) string {
	home := os.Getenv("HOME")
	displayFile := rcFile
	if home != "" && strings.HasPrefix(rcFile, home) {
		displayFile = "~" + rcFile[len(home):]
	}
	return fmt.Sprintf(`# Hash migration: sourcing %s config with compatibility filtering
# Run 'hash migrate generate' to create a standalone .hashrc
# Run 'hash migrate status' to see what was skipped
source %s
`, displayFile, rcFile)
}

// FormatHashrcCommentFiles returns the comment block for .hashrc with multiple source files.
// Note: We don't include source commands here because mvdan/sh's internal source
// builtin parses with POSIX mode. Instead, Hash sources migration files directly
// at startup using SourceWithCompat which uses LangBash parsing.
func FormatHashrcCommentFiles(files []string) string {
	home := os.Getenv("HOME")
	var b strings.Builder

	b.WriteString("# Hash migration: shell config loaded from:\n")
	for _, file := range files {
		displayFile := file
		if home != "" && strings.HasPrefix(file, home) {
			displayFile = "~" + file[len(home):]
		}
		b.WriteString(fmt.Sprintf("#   %s\n", displayFile))
	}
	b.WriteString("#\n")
	b.WriteString("# Files are sourced at startup with bash syntax support.\n")
	b.WriteString("# Run 'hash migrate generate' to create a standalone .hashrc\n")
	b.WriteString("# Run 'hash migrate status' to see what was skipped\n")
	b.WriteString("#\n")
	b.WriteString("# Add your own customizations below:\n\n")

	return b.String()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
