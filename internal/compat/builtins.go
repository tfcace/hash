// internal/compat/builtins.go
package compat

import "strings"

// NoopBuiltinFunc is a function that handles a no-op builtin.
// It receives the arguments and a report to log the skip.
type NoopBuiltinFunc func(args []string, report *Report) error

// NoopBuiltins returns a map of zsh-specific commands that should be no-ops.
func NoopBuiltins() map[string]NoopBuiltinFunc {
	return map[string]NoopBuiltinFunc{
		"bindkey":    noopBindkey,
		"setopt":     noopSetopt,
		"unsetopt":   noopSetopt, // Same handler
		"autoload":   noopAutoload,
		"compdef":    noopCompdef,
		"zstyle":     noopZstyle,
		"typeset":    noopTypeset,
		"zmodload":   noopGeneric("zsh module loading"),
		"zle":        noopGeneric("zsh line editor"),
		"compinit":   noopGeneric("zsh completion init"),
		"promptinit": noopGeneric("zsh prompt init"),
	}
}

// IsNoopBuiltin checks if a command is a no-op builtin.
func IsNoopBuiltin(cmd string) bool {
	_, ok := NoopBuiltins()[cmd]
	return ok
}

func noopBindkey(args []string, report *Report) error {
	if report != nil {
		report.AddSkipped(0, "bindkey "+strings.Join(args, " "), "zsh key binding (use Hash keybindings config)")
	}
	return nil
}

func noopSetopt(args []string, report *Report) error {
	if report != nil {
		report.AddSkipped(0, "setopt "+strings.Join(args, " "), "zsh shell option (use Hash config)")
	}
	return nil
}

func noopAutoload(args []string, report *Report) error {
	if report != nil {
		report.AddSkipped(0, "autoload "+strings.Join(args, " "), "zsh function autoloading")
	}
	return nil
}

func noopCompdef(args []string, report *Report) error {
	if report != nil {
		report.AddSkipped(0, "compdef "+strings.Join(args, " "), "zsh completion definition (use Hash completion)")
	}
	return nil
}

func noopZstyle(args []string, report *Report) error {
	if report != nil {
		report.AddSkipped(0, "zstyle "+strings.Join(args, " "), "zsh style configuration")
	}
	return nil
}

func noopTypeset(args []string, report *Report) error {
	// typeset is partially supported in bash mode, only skip zsh-specific usage
	if report != nil {
		report.AddSkipped(0, "typeset "+strings.Join(args, " "), "zsh variable declaration")
	}
	return nil
}

func noopGeneric(reason string) NoopBuiltinFunc {
	return func(args []string, report *Report) error {
		if report != nil {
			report.AddSkipped(0, strings.Join(args, " "), reason)
		}
		return nil
	}
}
