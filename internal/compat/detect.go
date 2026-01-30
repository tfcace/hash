// internal/compat/detect.go
package compat

import (
	"os"
	"path/filepath"
)

// shellFromEnv extracts the shell name from a SHELL environment value.
func shellFromEnv(shellEnv string) string {
	if shellEnv == "" {
		return ""
	}
	// Get basename (e.g., /bin/zsh -> zsh)
	name := filepath.Base(shellEnv)
	// Handle common shells
	switch name {
	case "zsh", "bash", "sh", "fish", "ksh":
		return name
	default:
		return name
	}
}

// detectRCFile returns the path to the rc file for a given shell.
func detectRCFile(shell, home string) string {
	switch shell {
	case "zsh":
		return filepath.Join(home, ".zshrc")
	case "bash":
		// Try .bashrc first, then .bash_profile
		bashrc := filepath.Join(home, ".bashrc")
		if _, err := os.Stat(bashrc); err == nil {
			return bashrc
		}
		return filepath.Join(home, ".bash_profile")
	default:
		return ""
	}
}

// detectProfileFile returns the path to the profile file for a given shell.
func detectProfileFile(shell, home string) string {
	switch shell {
	case "zsh":
		return filepath.Join(home, ".zprofile")
	case "bash":
		// Try .bash_profile first, then .profile
		bashProfile := filepath.Join(home, ".bash_profile")
		if _, err := os.Stat(bashProfile); err == nil {
			return bashProfile
		}
		return filepath.Join(home, ".profile")
	default:
		return ""
	}
}

// detectEnvFile returns the path to the env file for a given shell.
// The env file (.zshenv) is sourced for ALL shell invocations.
func detectEnvFile(shell, home string) string {
	switch shell {
	case "zsh":
		return filepath.Join(home, ".zshenv")
	case "bash":
		// Bash doesn't have an equivalent to .zshenv
		return ""
	default:
		return ""
	}
}

// ShellFiles contains the config files for a shell.
type ShellFiles struct {
	Shell       string
	EnvFile     string // Environment file sourced for all invocations (e.g., .zshenv)
	ProfileFile string // Login shell config (e.g., .zprofile, .bash_profile)
	RCFile      string // Interactive shell config (e.g., .zshrc, .bashrc)
}

// Files returns a slice of existing files in source order (env first, then profile, then rc).
func (sf ShellFiles) Files() []string {
	var files []string
	if sf.EnvFile != "" {
		if _, err := os.Stat(sf.EnvFile); err == nil {
			files = append(files, sf.EnvFile)
		}
	}
	if sf.ProfileFile != "" {
		if _, err := os.Stat(sf.ProfileFile); err == nil {
			files = append(files, sf.ProfileFile)
		}
	}
	if sf.RCFile != "" {
		if _, err := os.Stat(sf.RCFile); err == nil {
			files = append(files, sf.RCFile)
		}
	}
	return files
}

// DetectPreviousShellFiles detects the user's previous shell and all its config files.
func DetectPreviousShellFiles() ShellFiles {
	home, err := os.UserHomeDir()
	if err != nil {
		return ShellFiles{}
	}

	// Try $SHELL environment variable first
	shell := shellFromEnv(os.Getenv("SHELL"))
	if shell != "" && shell != "hash" {
		files := ShellFiles{
			Shell:       shell,
			EnvFile:     detectEnvFile(shell, home),
			ProfileFile: detectProfileFile(shell, home),
			RCFile:      detectRCFile(shell, home),
		}
		if len(files.Files()) > 0 {
			return files
		}
	}

	// Fallback: check which files exist
	for _, s := range []string{"zsh", "bash"} {
		files := ShellFiles{
			Shell:       s,
			EnvFile:     detectEnvFile(s, home),
			ProfileFile: detectProfileFile(s, home),
			RCFile:      detectRCFile(s, home),
		}
		if len(files.Files()) > 0 {
			return files
		}
	}

	return ShellFiles{}
}
