// cmd/hash/migrate.go
package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/tfcace/hash/internal/compat"
	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/tilde"
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
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("no migration found (run 'hash migrate' first)")
	}
	if err != nil {
		return fmt.Errorf("%w (run 'hash migrate' to import again)", err)
	}

	names := make([]string, len(state.Paths))
	for i, p := range state.Paths {
		names[i] = tilde.Abbrev(p)
	}
	source := strings.Join(names, " + ")
	if state.Status == compat.StatusDeclined {
		fmt.Fprintf(w, "Migration from %s (shell: %s) was declined.\n", source, state.SourceShell)
		fmt.Fprintf(w, "Run 'hash migrate' to import it.\n")
		return nil
	}

	fmt.Fprintf(w, "Migration from %s (shell: %s)\n\n", source, state.SourceShell)
	// The files are filtered again so what is shown is what the next
	// startup loads and skips; no report: no file loads.
	_, report, notLoaded := compat.FilterFiles(state.Paths, state.SourceShell, migrateShellDialect())
	if report == nil {
		fmt.Fprintf(w, "Imported: nothing, as no migrated file loads\n")
	} else {
		summary := report.Summary
		fmt.Fprintf(w, "Imported:\n")
		fmt.Fprintf(w, "  %s\n", compat.Count(summary.Aliases, "alias", "aliases"))
		fmt.Fprintf(w, "  %s\n", compat.Count(summary.Exports, "environment variable", "environment variables"))
		fmt.Fprintf(w, "  %s\n", compat.Count(summary.Functions, "function", "functions"))
		fmt.Fprintf(w, "\nSkipped: %s\n", compat.Count(summary.Skipped, "item", "items"))
		for _, item := range report.SkippedItems {
			fmt.Fprintf(w, "  %s:%d: %s (%s)\n", tilde.Abbrev(item.File), item.Line, item.Content, item.Reason)
		}
	}
	// The files the migrated files source are known once they ran: as the
	// last startup found them.
	notLoadedFiles := state.Import.SourcedNotLoaded
	if report != nil {
		for _, n := range notLoadedFiles {
			report.NotLoad(n)
		}
		notLoadedFiles = report.NotLoaded
	}
	printNotLoaded(w, notLoaded, notLoadedFiles)
	fmt.Fprintf(w, "\nLast import: %s\n", state.Import.Time.Format("2006-01-02 15:04:05"))

	return nil
}

// printNotLoaded lists the migrated files that did not load: those the
// filter failed on (errs), and those in which a statement does not parse.
func printNotLoaded(w io.Writer, errs []error, files []compat.NotLoaded) {
	if len(errs) == 0 && len(files) == 0 {
		return
	}
	fmt.Fprintf(w, "\nNot loaded:\n")
	for _, err := range errs {
		fmt.Fprintf(w, "  %v\n", err)
	}
	for _, n := range files {
		fmt.Fprintf(w, "  %s\n", n)
	}
}

const migrateHelp = `
Hash will source your existing config with compatibility filtering:

  ✓ Aliases (e.g., alias ll='ls -la')
  ✓ Environment variables (e.g., export EDITOR=vim)
  ✓ Functions (POSIX-compatible)
  ✓ PATH modifications (including Homebrew from .zprofile)

Hash filters using your configured shell.dialect. Bash mode skips
common zsh-only init/eval lines; zsh mode preserves zsh syntax where
possible. Unsupported shell/editor builtins such as bindkey, setopt,
and compdef remain compatibility no-ops.

Run 'hash migrate status' to see what was skipped.

Your original config files are not modified - Hash sources them
directly with a compatibility layer.
`

func runMigrateInteractive() int {
	return migrateInteractive(os.Stdin, os.Stdout, compat.DefaultStatePath())
}

// migrateInteractive offers to import the previous shell's config. Answering
// no records a decline, except that an existing import is kept: "no" to a
// re-run of `hash migrate` means "change nothing", not "stop loading my config".
func migrateInteractive(in io.Reader, out io.Writer, statePath string) int {
	shellFiles := compat.DetectPreviousShellFiles()
	files := shellFiles.Files()
	if shellFiles.Shell == "" || len(files) == 0 {
		fmt.Fprintln(out, "No previous shell configuration found.")
		fmt.Fprintln(out, "Use 'hash migrate --from zsh' or 'hash migrate --from bash' to import manually.")
		return 0
	}

	_, _ = lipgloss.Fprintln(out, compat.FormatWelcomePromptFiles(shellFiles))

	switch compat.AskMigration(compat.LineReader(in), out, "Choice", migrateHelp) {
	case compat.DecisionImport:
		return importMigration(out, statePath, shellFiles.Shell, files)
	case compat.DecisionDecline:
		if state, err := compat.LoadState(statePath); err == nil && state.Status == compat.StatusImported {
			fmt.Fprintln(out, "\nNothing changed: your existing migration is still loaded at startup.")
			return 0
		}
		if err := compat.RecordDecline(statePath, shellFiles.Shell, files); err != nil {
			fmt.Fprintf(os.Stderr, "hash migrate: could not save the answer: %v\n", err)
			return 1
		}
		fmt.Fprintln(out, "\nNo problem! You can run 'hash migrate' anytime to load settings.")
		return 0
	default:
		fmt.Fprintln(os.Stderr, "hash migrate: no answer, nothing changed")
		return 1
	}
}

// importMigration filters files to report what will load and records the
// import; the files themselves are sourced by the next interactive startup.
// Typed in hash, it runs in a child process: nothing loads into the
// session it was typed in, so it says the import loads in new sessions.
func importMigration(out io.Writer, statePath, shell string, paths []string) int {
	fmt.Fprint(out, "\nAnalyzing config files... ")

	files, report, errs := compat.FilterFiles(paths, shell, migrateShellDialect())
	for _, err := range errs {
		fmt.Fprintf(os.Stderr, "\nhash migrate: %v\n", err)
	}
	if report == nil {
		fmt.Fprintf(os.Stderr, "\nhash migrate: no files could be loaded\n")
		return 1
	}

	fmt.Fprintln(out, "done!")
	fmt.Fprintln(out)
	inHash := os.Getenv("HASH_SHELL") == "1"
	_, _ = lipgloss.Fprintln(out, compat.FormatImportSummary(report, inHash))

	created, hashrcErr, err := compat.RecordImport(statePath, shell, files, report)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash migrate: could not save the migration, nothing was imported: %v\n", err)
		return 1
	}
	if created {
		fmt.Fprintf(out, "Created %s\n", compat.HashrcPath())
	}
	if hashrcErr != nil {
		fmt.Fprintf(os.Stderr, "hash migrate: warning: %v\n", hashrcErr)
	}
	if inHash {
		fmt.Fprintln(out, "Loads in new hash sessions: open a new tab, or run 'exec hash -l'.")
	}
	return 0
}

func runMigrateFrom(shell string, _ []string) int {
	home, _ := os.UserHomeDir()
	var rcFile string

	switch shell {
	case "zsh":
		rcFile = filepath.Join(home, ".zshrc")
	case "bash":
		rcFile = filepath.Join(home, ".bashrc")
	default:
		fmt.Fprintf(os.Stderr, "hash migrate: unsupported shell %q (use zsh or bash)\n", shell)
		return 1
	}

	if _, err := os.Stat(rcFile); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "hash migrate: %s not found\n", rcFile)
		return 1
	}

	return importMigration(os.Stdout, compat.DefaultStatePath(), shell, []string{rcFile})
}

// migrateShellDialect returns the configured parser dialect. Load always
// returns a usable config, keeping the sections that decoded, so a broken
// unrelated section must not reset the dialect to bash.
func migrateShellDialect() string {
	cfg := config.Load(config.Dir())
	if strings.TrimSpace(cfg.Shell.Dialect) == "" {
		return "bash"
	}
	return cfg.Shell.Dialect
}
