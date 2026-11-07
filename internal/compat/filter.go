// internal/compat/filter.go
package compat

import (
	"errors"
	"strings"

	"github.com/tfcace/hash/internal/shparse"
	"mvdan.cc/sh/v3/syntax"
)

// parseErrorLine returns the line a parse error points at, or 0.
func parseErrorLine(err error) int {
	return int(parseErrorPos(err).Line())
}

// parseErrorOffset returns the byte offset a parse error points at, or -1.
// Unlike its column, which stops at 16383, the offset is exact on any line.
func parseErrorOffset(err error) int {
	if pos := parseErrorPos(err); pos.IsValid() {
		return int(pos.Offset())
	}
	return -1
}

// parseErrorPos returns the position a parse error points at, or the zero
// Pos, which is not valid.
func parseErrorPos(err error) syntax.Pos {
	var perr syntax.ParseError
	var lerr syntax.LangError
	switch {
	case errors.As(err, &perr):
		return perr.Pos
	case errors.As(err, &lerr):
		return lerr.Pos
	}
	return syntax.Pos{}
}

// parseErrorText returns a parse error's message without its position.
func parseErrorText(err error) string {
	msg := err.Error()
	if parseErrorLine(err) > 0 {
		if _, rest, ok := strings.Cut(msg, ": "); ok {
			return rest
		}
	}
	return msg
}

// parseRC parses filtered rc content the way the executor will.
func parseRC(content, dialect string) (*syntax.File, error) {
	return shparse.Parse(syntax.NewParser(syntax.Variant(langVariantForDialect(dialect))), content, "")
}

// trackImport records an alias or export command, trimmed, in report.
func trackImport(report *Report, trimmed string) {
	if rest, ok := strings.CutPrefix(trimmed, "alias "); ok {
		if name, value, ok := strings.Cut(rest, "="); ok {
			report.AddImported(ItemAlias, strings.TrimSpace(name), strings.Trim(value, "'\""))
		}
	} else if rest, ok := strings.CutPrefix(trimmed, "export "); ok {
		name, value, _ := strings.Cut(rest, "=")
		report.AddImported(ItemExport, strings.TrimSpace(name), value)
	}
}
