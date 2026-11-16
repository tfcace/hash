// internal/compat/prompt.go
package compat

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/tfcace/hash/internal/prompt"
	"github.com/tfcace/hash/internal/termtext"
	"github.com/tfcace/hash/internal/tilde"
)

// migrationFrame draws rows on the frame every hash surface shares: a
// title row, the rows behind a bar (one that starts a section draws its
// own "├"), and the foot.
type migrationFrame struct {
	accent, dim, success, warning lipgloss.Style
	rows                          []string
}

func newMigrationFrame(p prompt.Palette, summary string) *migrationFrame {
	f := &migrationFrame{
		accent:  lipgloss.NewStyle().Foreground(lipgloss.Color(p.Primary)),
		dim:     lipgloss.NewStyle().Foreground(lipgloss.Color(p.Dim)),
		success: lipgloss.NewStyle().Foreground(lipgloss.Color(p.Success)),
		warning: lipgloss.NewStyle().Foreground(lipgloss.Color(p.Warning)),
	}
	f.rows = []string{f.dim.Render("┌ ") + f.accent.Bold(true).Render("migrate") + f.dim.Render(" · ") + summary}
	return f
}

func (f *migrationFrame) row(text string)     { f.rows = append(f.rows, f.dim.Render("│")+" "+text) }
func (f *migrationFrame) section(name string) { f.rows = append(f.rows, f.dim.Render("├ "+name)) }

// foot closes the frame with its hints and returns it, with no newline
// after its last row.
func (f *migrationFrame) foot(hints ...string) string {
	return strings.Join(append(f.rows, f.dim.Render("└─ "+strings.Join(hints, " · "))), "\n")
}

// FormatWelcomePromptFiles returns the offer to load the detected config
// files, drawn in the colors of p. The question itself is AskMigration's.
func FormatWelcomePromptFiles(files ShellFiles, p prompt.Palette) string {
	f := newMigrationFrame(p, files.Shell+" settings found")
	for _, file := range files.Files() {
		f.row(lipgloss.NewStyle().Bold(true).Render(termtext.OneLine(tilde.Abbrev(file))))
	}
	f.row("Load the settings hash understands? Your files stay as they are.")

	other := "bash"
	if files.Shell == "bash" {
		other = "zsh"
	}
	return f.foot("y load", "n start fresh", "? what this does", "later: hash migrate --from "+other)
}

// skippedShown is how many skipped items the summary lists.
const skippedShown = 3

// FormatImportSummary returns a summary of the migration, drawn in the
// colors of p: what loaded, or, when it loads later (not into the shell
// that imports it), what will load.
func FormatImportSummary(report *Report, later bool, p prompt.Palette) string {
	names := make([]string, len(report.Files))
	for i, file := range report.Files {
		names[i] = termtext.OneLine(tilde.Abbrev(file))
	}
	heading := "loaded from "
	if later {
		heading = "will load from "
	}
	f := newMigrationFrame(p, heading+strings.Join(names, " + "))

	for _, kind := range []struct {
		n            int
		one, several string
	}{
		{report.Summary.Aliases, "alias", "aliases"},
		{report.Summary.Exports, "environment variable", "environment variables"},
		{report.Summary.Functions, "function", "functions"},
	} {
		if kind.n > 0 {
			f.row(f.success.Render("✓") + " " + Count(kind.n, kind.one, kind.several))
		}
	}

	if report.Summary.Skipped > 0 {
		f.section("skipped " + Count(report.Summary.Skipped, "item", "items"))
		for i, item := range report.SkippedItems {
			if i == skippedShown {
				f.row(f.dim.Render(fmt.Sprintf("… %d more", len(report.SkippedItems)-skippedShown)))
				break
			}
			// A config file's own text: drawn inert, on one row.
			f.row(f.dim.Render(fmt.Sprintf("line %d: %s", item.Line, termtext.Truncate(termtext.OneLine(item.Content), 50, "..."))))
		}
	}
	for _, n := range report.NotLoaded {
		f.row(f.warning.Render(termtext.OneLine(n.String())))
	}
	return f.foot("hash migrate status for the full list")
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
