// internal/compat/detect.go
package compat

import (
	"os"
	"path/filepath"
	"strings"
)

// shellFromEnv extracts the shell name from a SHELL environment value.
func shellFromEnv(shellEnv string) string {
	if shellEnv == "" {
		return ""
	}
	return filepath.Base(shellEnv) // /bin/zsh -> zsh
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

// ShellForPath returns the shell a startup file at path is written for,
// from its extension (.zsh, .bash, .sh: zsh-compat.sh is bash), or else its
// name, or "" for one of hash's own (.hashrc) or an unknown one. zsh's files
// go by other names too: /etc/zshrc, .zshrc.local, .zsh_aliases.
func ShellForPath(path string) string {
	switch filepath.Ext(path) {
	case ".zsh":
		return "zsh"
	case ".bash", ".sh":
		return "bash"
	}
	return startupFileShell(path)
}

// startupFileShell returns the shell whose startup file the file at path
// is, by its name (.bashrc, .profile, .zshrc, /etc/zshrc, .zshrc.local), or
// "": a script of the shell's (nvm.sh) is not one.
func startupFileShell(path string) string {
	base := filepath.Base(path)
	name := strings.TrimPrefix(base, ".")
	stem, _, _ := strings.Cut(name, ".")
	switch {
	case strings.HasPrefix(name, "zsh"), stem == "zprofile", stem == "zlogin", stem == "zlogout":
		return "zsh"
	}
	switch base {
	case ".bashrc", ".bash_profile", ".bash_login", ".profile":
		return "bash"
	}
	return ""
}

// ReadBy is which shells read a startup file.
type ReadBy int

const (
	// ReadByEvery: every shell, as zsh reads ~/.zshenv.
	ReadByEvery ReadBy = iota
	// ReadByLogin: a login shell (~/.zprofile, ~/.bash_profile, ~/.profile).
	ReadByLogin
	// ReadByInteractive: an interactive shell (~/.zshrc, ~/.bashrc, and a
	// file of any other name).
	ReadByInteractive
)

// ReadByPath returns which shells read the startup file at path, from its
// name.
func ReadByPath(path string) ReadBy {
	switch filepath.Base(path) {
	case ".zshenv":
		return ReadByEvery
	case ".zprofile", ".zlogin", ".bash_profile", ".bash_login", ".profile":
		return ReadByLogin
	}
	return ReadByInteractive
}
