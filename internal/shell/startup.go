package shell

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tfcace/hash/internal/compat"
)

// runStartup executes all startup files and commands based on shell mode.
// Order: migration files -> login files -> profile commands -> interactive files -> rc commands -> init_commands
func (s *Shell) runStartup(ctx context.Context) error {
	// Check for first-run migration before anything else
	s.checkFirstRunMigration(ctx)

	// Source migration files (from previous shell) with compatibility filtering
	// This runs after first-run check so files are available on subsequent startups
	s.sourceMigrationFiles(ctx)

	// Check if source file changed since last import
	s.checkSourceFileChanged()

	if s.config == nil {
		return nil
	}

	// 1. Login shell: source login files (e.g., /etc/profile, ~/.profile, ~/.hash_profile)
	if s.mode.Login {
		for _, file := range s.config.Shell.StartupFiles.Login {
			if err := s.sourceFile(ctx, file); err != nil {
				// Log but don't fail on missing optional files
				if !os.IsNotExist(err) {
					fmt.Fprintf(os.Stderr, "hash: %s: %v\n", file, err)
				}
			}
		}

		// Run profile commands from config
		for _, cmd := range s.config.Shell.ProfileCommands {
			if err := s.runStartupCommand(ctx, cmd); err != nil {
				fmt.Fprintf(os.Stderr, "hash: profile: %v\n", err)
			}
		}
	}

	// 2. Interactive shell: source rc files (~/.hashrc)
	if s.mode.Interactive {
		for _, file := range s.config.Shell.StartupFiles.Interactive {
			if err := s.sourceFile(ctx, file); err != nil {
				if !os.IsNotExist(err) {
					fmt.Fprintf(os.Stderr, "hash: %s: %v\n", file, err)
				}
			}
		}

		// Run rc commands from config
		for _, cmd := range s.config.Shell.RCCommands {
			if err := s.runStartupCommand(ctx, cmd); err != nil {
				fmt.Fprintf(os.Stderr, "hash: rc: %v\n", err)
			}
		}
	}

	// 3. Always run init_commands (legacy, for backwards compatibility)
	// Use runInitCommands which handles builtins properly
	return s.runInitCommands(ctx)
}

// sourceFile reads and executes a shell script file.
func (s *Shell) sourceFile(ctx context.Context, path string) error {
	// Expand ~ to home directory
	if strings.HasPrefix(path, "~") {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, path[1:])
	}

	// Check if file exists
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("is a directory")
	}

	// Read file content
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// Execute as shell commands
	_, err = s.executor.Execute(ctx, string(content), os.Stdout, os.Stderr)
	return err
}

// runStartupCommand executes a single startup command.
func (s *Shell) runStartupCommand(ctx context.Context, command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}

	_, err := s.executor.Execute(ctx, command, os.Stdout, os.Stderr)
	return err
}

// checkFirstRunMigration checks if we should show the migration prompt.
// Called at the start of runStartup for interactive shells.
func (s *Shell) checkFirstRunMigration(ctx context.Context) {
	if !s.mode.Interactive {
		return
	}

	shouldShow, _, _ := compat.ShouldShowMigrationPrompt()
	if !shouldShow {
		return
	}

	// Get all shell files
	shellFiles := compat.DetectPreviousShellFiles()
	files := shellFiles.Files()
	if len(files) == 0 {
		return
	}

	// Show the welcome prompt
	fmt.Println(compat.FormatWelcomePromptFiles(shellFiles))

	// For now, auto-load (interactive prompting requires terminal handling)
	// TODO: Add proper interactive prompt with readline
	fmt.Print("\nAuto-loading settings... ")

	// Filter and source all files, merge reports
	var totalReport *compat.Report
	for _, file := range files {
		// Filter file content (skip zsh builtins, etc.)
		filtered, report, err := compat.FilterWithCompat(file, shellFiles.Shell)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nhash: %s: %v\n", file, err)
			continue
		}

		// Execute through our executor (parses with LangBash, persists to shell)
		if s.executor != nil {
			_, err = s.executor.Execute(ctx, filtered, os.Stdout, os.Stderr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\nhash: %s: %v\n", file, err)
			}
		}

		if totalReport == nil {
			totalReport = report
		} else {
			totalReport.Summary.Aliases += report.Summary.Aliases
			totalReport.Summary.Exports += report.Summary.Exports
			totalReport.Summary.Functions += report.Summary.Functions
			totalReport.Summary.Skipped += report.Summary.Skipped
			totalReport.ImportedItems = append(totalReport.ImportedItems, report.ImportedItems...)
			totalReport.SkippedItems = append(totalReport.SkippedItems, report.SkippedItems...)
		}
	}

	if totalReport == nil {
		fmt.Fprintf(os.Stderr, "\nhash: no files could be loaded\n")
		return
	}

	fmt.Println("done!")
	fmt.Println()
	totalReport.SourceFile = strings.Join(files, " + ")
	fmt.Println(compat.FormatImportSummary(totalReport))

	// Create .hashrc with source lines
	home, _ := os.UserHomeDir()
	hashrcPath := filepath.Join(home, ".hashrc")
	content := compat.FormatHashrcCommentFiles(files)
	if err := os.WriteFile(hashrcPath, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "hash: could not create .hashrc: %v\n", err)
	}

	// Save state
	state := &compat.State{
		SourceFile:  strings.Join(files, ", "),
		SourceFiles: files, // Individual paths for sourcing
		SourceShell: shellFiles.Shell,
		SourceMtime: totalReport.SourceMtime,
		LastImport:  totalReport.ImportTime,
		Summary:     totalReport.Summary,
	}
	_ = state.Save(compat.DefaultStatePath())

	fmt.Println()
}

// checkSourceFileChanged checks if the migrated source file changed.
// Shows a one-line summary if it did.
func (s *Shell) checkSourceFileChanged() {
	if !s.mode.Interactive {
		return
	}

	statePath := compat.DefaultStatePath()
	changed, err := compat.CheckSourceChanged(statePath)
	if err != nil || !changed {
		return
	}

	state, err := compat.LoadState(statePath)
	if err != nil {
		return
	}
	fmt.Print(compat.FormatChangeNotice(state.SourceFile, state.Summary.Skipped))
}

// sourceMigrationFiles sources files from migration state with compatibility filtering.
// Uses FilterWithCompat for pre-processing and executes through the shell's executor
// so that aliases, exports, and functions persist to the shell session.
// Called on every startup for migrated shells.
func (s *Shell) sourceMigrationFiles(ctx context.Context) {
	if !s.mode.Interactive {
		return
	}

	statePath := compat.DefaultStatePath()
	state, err := compat.LoadState(statePath)
	if err != nil || state.Declined {
		return
	}

	// Source each migration file with compatibility layer
	for _, file := range state.SourceFiles {
		if _, err := os.Stat(file); os.IsNotExist(err) {
			continue
		}

		// Filter the file content (skip zsh builtins, etc.)
		filtered, _, err := compat.FilterWithCompat(file, state.SourceShell)
		if err != nil {
			fmt.Fprintf(os.Stderr, "hash: %s: %v\n", file, err)
			continue
		}

		// Execute through our executor (parses with LangBash, persists to shell)
		if s.executor != nil {
			_, err = s.executor.Execute(ctx, filtered, os.Stdout, os.Stderr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "hash: %s: %v\n", file, err)
			}
		}
	}
}
