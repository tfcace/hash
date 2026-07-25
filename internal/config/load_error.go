package config

import (
	"errors"
	"fmt"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// LoadError describes a config.toml that could not be fully loaded.
// When BadSections is non-empty, only those sections fell back to defaults
// and everything else was kept; when it is empty, the whole file was
// unparseable and defaults apply.
type LoadError struct {
	Path        string   // config file path
	BadSections []string // sections reverted to defaults; empty means the whole file
	Detail      string   // human-readable position and cause
	Err         error    // underlying decode error
}

// Error returns a one-line summary.
func (e *LoadError) Error() string {
	if len(e.BadSections) == 0 {
		return fmt.Sprintf("config error in %s (%s): using defaults", e.Path, e.Detail)
	}
	return fmt.Sprintf("config error in %s (%s): defaults used for [%s]",
		e.Path, e.Detail, strings.Join(e.BadSections, "], ["))
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
	if len(e.BadSections) == 0 {
		b.WriteString("  The file could not be parsed; all settings use defaults.\n")
	} else {
		fmt.Fprintf(&b, "  Reverted to defaults: [%s]. All other settings were kept.\n",
			strings.Join(e.BadSections, "], ["))
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
