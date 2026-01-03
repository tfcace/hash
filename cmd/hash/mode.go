package main

import (
	"os"
	"path/filepath"
	"strings"
)

// ShellMode represents the startup mode of the shell.
type ShellMode struct {
	Login       bool // Login shell (reads profile files)
	Interactive bool // Interactive shell (has TTY, reads rc)
	Command     bool // Non-interactive command execution (-c)
}

// DetectMode determines the shell mode from arguments and environment.
func DetectMode(args []string) ShellMode {
	mode := ShellMode{}

	// Check argv[0] for leading dash (login shell convention)
	if len(args) > 0 {
		base := filepath.Base(args[0])
		if strings.HasPrefix(base, "-") {
			mode.Login = true
		}
	}

	return mode
}

// IsInteractive returns true if stdin/stdout are terminals.
func IsInteractive() bool {
	stdinInfo, _ := os.Stdin.Stat()
	stdoutInfo, _ := os.Stdout.Stat()

	stdinIsTerm := (stdinInfo.Mode() & os.ModeCharDevice) != 0
	stdoutIsTerm := (stdoutInfo.Mode() & os.ModeCharDevice) != 0

	return stdinIsTerm && stdoutIsTerm
}
