package config

import (
	"errors"
	"fmt"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// LoadErrorKind is how loading config.toml went wrong. A missing file and a
// clean load are not errors: Config.LoadIssue is nil for both.
type LoadErrorKind int

const (
	// LoadReadFailed: the file exists but could not be read (permissions,
	// a directory in its place). All settings use defaults.
	LoadReadFailed LoadErrorKind = iota + 1
	// LoadSyntaxError: the file is not valid TOML. All settings use defaults.
	LoadSyntaxError
	// LoadPartial: the file parsed but some sections did not decode; those
	// (BadSections) use defaults and everything else was kept.
	LoadPartial
)

// LoadError describes a config.toml that could not be fully loaded.
type LoadError struct {
	Kind        LoadErrorKind
	Path        string   // config file path
	BadSections []string // LoadPartial only: sections reverted to defaults
	Detail      string   // human-readable position and cause
	Err         error    // underlying read or decode error
}

// Error returns a one-line summary.
func (e *LoadError) Error() string {
	switch e.Kind {
	case LoadReadFailed:
		return fmt.Sprintf("config error in %s (%s): could not be read, using defaults", e.Path, e.Detail)
	case LoadPartial:
		return fmt.Sprintf("config error in %s (%s): defaults used for [%s]",
			e.Path, e.Detail, strings.Join(e.BadSections, "], ["))
	default:
		return fmt.Sprintf("config error in %s (%s): using defaults", e.Path, e.Detail)
	}
}

// Unwrap exposes the underlying decode error.
func (e *LoadError) Unwrap() error {
	return e.Err
}

// Warning returns a multi-line, terminal-ready warning for startup.
func (e *LoadError) Warning() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\033[31m✗ Config error\033[0m in %s\n", e.Path)
	if e.Detail != "" {
		fmt.Fprintf(&b, "  %s\n", e.Detail)
	}
	switch e.Kind {
	case LoadReadFailed:
		b.WriteString("  The file could not be read; all settings use defaults.\n")
	case LoadPartial:
		fmt.Fprintf(&b, "  Reverted to defaults: [%s]. All other settings were kept.\n",
			strings.Join(e.BadSections, "], ["))
	default:
		b.WriteString("  The file could not be parsed; all settings use defaults.\n")
	}
	b.WriteString("  Run \033[36mstatus\033[0m in hash to review; fix the file to restore your settings.\n")
	return b.String()
}

// decodeErrorDetail extracts a position-annotated cause from a toml error.
func decodeErrorDetail(err error) string {
	var de *toml.DecodeError
	if errors.As(err, &de) {
		row, col := de.Position()
		return fmt.Sprintf("line %d, column %d: %s", row, col, de.Error())
	}
	return err.Error()
}
