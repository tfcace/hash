// cmd/hash/migrate.go
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tfcace/hash/internal/compat"
)

func printMigrateHelp() {
	fmt.Println(`Usage: hash migrate [command] [flags]

Commands:
  status      Show what was imported/skipped from last migration

Flags:
  --from <shell>    Import from specific shell (zsh, bash)

Examples:
  hash migrate              Interactive import prompt
  hash migrate --from zsh   Import from ~/.zshrc
  hash migrate status       Show migration report`)
}

func runMigrate(args []string) int {
	if len(args) == 0 {
		// Interactive migration
		return runMigrateInteractive()
	}

	switch args[0] {
	case "status":
		if err := runMigrateStatus(os.Stdout, compat.DefaultStatePath()); err != nil {
			fmt.Fprintf(os.Stderr, "hash migrate: %v\n", err)
			return 1
		}
		return 0
	case "--from":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "hash migrate: --from requires a shell name\n")
			return 1
		}
		return runMigrateFrom(args[1], args[2:])
	case "-h", "--help":
		printMigrateHelp()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "hash migrate: unknown command %q\n", args[0])
		printMigrateHelp()
		return 1
	}
}

func runMigrateStatus(w io.Writer, statePath string) error {
	state, err := compat.LoadState(statePath)
	if err != nil {
		return fmt.Errorf("no migration found (run 'hash migrate' first)")
	}

	fmt.Fprintf(w, "Migration from %s (shell: %s)\n\n", state.SourceFile, state.SourceShell)
	fmt.Fprintf(w, "Imported:\n")
	fmt.Fprintf(w, "  %d aliases\n", state.Summary.Aliases)
	fmt.Fprintf(w, "  %d environment variables\n", state.Summary.Exports)
	fmt.Fprintf(w, "  %d functions\n", state.Summary.Functions)
	fmt.Fprintf(w, "\nSkipped: %d items\n", state.Summary.Skipped)
	fmt.Fprintf(w, "\nLast import: %s\n", state.LastImport.Format("2006-01-02 15:04:05"))

	return nil
}

func runMigrateInteractive() int {
	shellFiles := compat.DetectPreviousShellFiles()
	if shellFiles.Shell == "" {
		fmt.Println("No previous shell configuration found.")
		fmt.Println("Use 'hash migrate --from zsh' or 'hash migrate --from bash' to import manually.")
		return 0
	}

	files := shellFiles.Files()
	if len(files) == 0 {
		fmt.Println("No previous shell configuration found.")
		return 0
	}

	fmt.Println(compat.FormatWelcomePromptFiles(shellFiles))

	// Read user input
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("\nChoice [Y/n/?]: ")
		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintf(os.Stderr, "hash migrate: %v\n", err)
			return 1
		}

		input = strings.TrimSpace(strings.ToLower(input))

		switch input {
		case "", "y", "yes":
			// Proceed with migration
			return doMigrationFiles(shellFiles)

		case "n", "no":
			// User declined - save state so we don't ask again
			state := &compat.State{
				SourceFile:  strings.Join(files, ", "),
				SourceFiles: files,
				SourceShell: shellFiles.Shell,
				Declined:    true,
			}
			_ = state.Save(compat.DefaultStatePath())
			fmt.Println("\nNo problem! You can run 'hash migrate' anytime to load settings.")
			return 0

		case "?":
			fmt.Println(`
Hash will source your existing config with compatibility filtering:

  ✓ Aliases (e.g., alias ll='ls -la')
  ✓ Environment variables (e.g., export EDITOR=vim)
  ✓ Functions (POSIX-compatible)
  ✓ PATH modifications (including Homebrew from .zprofile)

Zsh-specific features like bindkey, setopt, compdef are silently
skipped. Run 'hash migrate status' to see what was skipped.

Your original config files are not modified - Hash sources them
directly with a compatibility layer.`)

		default:
			fmt.Println("Please enter Y, n, or ?")
		}
	}
}

func doMigrationFiles(shellFiles compat.ShellFiles) int {
	files := shellFiles.Files()
	fmt.Print("\nAnalyzing config files... ")

	// Filter all files and merge reports (actual execution happens at shell startup)
	var totalReport *compat.Report
	for _, file := range files {
		_, report, err := compat.FilterWithCompat(file, shellFiles.Shell)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nhash migrate: %s: %v\n", file, err)
			continue
		}
		if totalReport == nil {
			totalReport = report
		} else {
			// Merge summaries
			totalReport.Summary.Aliases += report.Summary.Aliases
			totalReport.Summary.Exports += report.Summary.Exports
			totalReport.Summary.Functions += report.Summary.Functions
			totalReport.Summary.Skipped += report.Summary.Skipped
			totalReport.ImportedItems = append(totalReport.ImportedItems, report.ImportedItems...)
			totalReport.SkippedItems = append(totalReport.SkippedItems, report.SkippedItems...)
		}
	}

	if totalReport == nil {
		fmt.Fprintf(os.Stderr, "\nhash migrate: no files could be loaded\n")
		return 1
	}

	fmt.Println("done!")
	fmt.Println()
	totalReport.SourceFile = strings.Join(files, " + ")
	fmt.Println(compat.FormatImportSummary(totalReport))

	// Create .hashrc with source lines for all files
	home, _ := os.UserHomeDir()
	hashrcPath := filepath.Join(home, ".hashrc")
	content := compat.FormatHashrcCommentFiles(files)
	if err := os.WriteFile(hashrcPath, []byte(content), 0o644); err != nil { //nolint:gosec // G306: user shell config file
		fmt.Fprintf(os.Stderr, "hash migrate: could not create .hashrc: %v\n", err)
	} else {
		fmt.Printf("Created %s\n", hashrcPath)
	}

	// Save state
	state := &compat.State{
		SourceFile:  strings.Join(files, ", "),
		SourceFiles: files,
		SourceShell: shellFiles.Shell,
		SourceMtime: totalReport.SourceMtime,
		LastImport:  totalReport.ImportTime,
		Summary:     totalReport.Summary,
	}
	if err := state.Save(compat.DefaultStatePath()); err != nil {
		fmt.Fprintf(os.Stderr, "hash migrate: warning: could not save state: %v\n", err)
	}

	return 0
}

func runMigrateFrom(shell string, args []string) int {
	home, _ := os.UserHomeDir()
	var rcFile string

	switch shell {
	case "zsh":
		rcFile = home + "/.zshrc"
	case "bash":
		rcFile = home + "/.bashrc"
	default:
		fmt.Fprintf(os.Stderr, "hash migrate: unsupported shell %q (use zsh or bash)\n", shell)
		return 1
	}

	if _, err := os.Stat(rcFile); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "hash migrate: %s not found\n", rcFile)
		return 1
	}

	_, report, err := compat.FilterWithCompat(rcFile, shell)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash migrate: %v\n", err)
		return 1
	}

	fmt.Println(compat.FormatImportSummary(report))

	// Save state
	state := &compat.State{
		SourceFile:  rcFile,
		SourceFiles: []string{rcFile},
		SourceShell: shell,
		SourceMtime: report.SourceMtime,
		LastImport:  report.ImportTime,
		Summary:     report.Summary,
	}
	if err := state.Save(compat.DefaultStatePath()); err != nil {
		fmt.Fprintf(os.Stderr, "hash migrate: warning: could not save state: %v\n", err)
	}

	return 0
}
