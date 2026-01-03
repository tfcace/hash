package shell

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// runStartup executes all startup files and commands based on shell mode.
// Order: login files -> profile commands -> interactive files -> rc commands -> init_commands
func (s *Shell) runStartup(ctx context.Context) error {
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
