package plugin

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

type simpleCommandShape struct {
	line string
	args []*syntax.Word
	lits []string
}

// ValidateCommandSuggestion verifies that a complete editor suggestion is
// syntactically valid for the active shell dialect. Unlike a correction, a
// learned command may contain a compound command; the editor still enforces
// the language-neutral single-line and control checks before calling here.
func ValidateCommandSuggestion(candidate, dialect string) error {
	if strings.TrimSpace(candidate) == "" {
		return fmt.Errorf("suggestion is empty")
	}
	lang := syntax.LangBash
	if strings.EqualFold(strings.TrimSpace(dialect), "zsh") {
		lang = syntax.LangZsh
	} else if dialect != "" && !strings.EqualFold(strings.TrimSpace(dialect), "bash") {
		return fmt.Errorf("unsupported dialect %q", dialect)
	}
	file, err := syntax.NewParser(syntax.Variant(lang)).Parse(strings.NewReader(candidate), "suggestion")
	if err != nil {
		return fmt.Errorf("parse suggestion: %w", err)
	}
	if len(file.Stmts) == 0 {
		return fmt.Errorf("suggestion has no command")
	}
	return nil
}

// ValidateCommandCorrection requires an identical top-level simple-command
// structure with exactly one eligible static token changed. All assignments,
// whitespace, quoting around unchanged tokens, and redirections must remain
// byte-for-byte identical.
func ValidateCommandCorrection(original, candidate, dialect string) error { //nolint:gocyclo // conservative validation keeps every rejection rule explicit
	before, err := parseSimpleCommandShape(original, dialect)
	if err != nil {
		return err
	}
	after, err := parseSimpleCommandShape(candidate, dialect)
	if err != nil {
		return err
	}
	if len(before.args) != len(after.args) {
		return fmt.Errorf("argument count changed")
	}
	if len(before.args) == 0 || unsafeCorrectionWrapper(before.lits[0]) {
		return fmt.Errorf("privileged or empty command is ineligible")
	}

	changed := -1
	for i := range before.args {
		beforeText := sourceForWord(before.line, before.args[i])
		afterText := sourceForWord(after.line, after.args[i])
		if beforeText != afterText {
			if changed >= 0 {
				return fmt.Errorf("more than one token changed")
			}
			changed = i
		}
	}
	if changed < 0 {
		return fmt.Errorf("correction is unchanged")
	}
	if changed > 1 && !strings.HasPrefix(before.lits[changed], "--") {
		return fmt.Errorf("changed token is not an executable, subcommand, or long flag")
	}
	if strings.HasPrefix(before.lits[changed], "-") && !strings.HasPrefix(before.lits[changed], "--") {
		return fmt.Errorf("short flags are ineligible")
	}
	for i, value := range before.lits {
		if i != changed && value == before.lits[changed] {
			return fmt.Errorf("changed token is ambiguous")
		}
	}
	if before.lits[changed] == "" || after.lits[changed] == "" {
		return fmt.Errorf("changed token is dynamic or quoted")
	}

	// The byte ranges outside each argument must be identical. This covers
	// assignments, redirections, and all formatting without reconstructing the
	// user's command.
	beforeCursor, afterCursor := 0, 0
	for i := range before.args {
		beforeStart, beforeEnd := wordOffsets(before.args[i])
		afterStart, afterEnd := wordOffsets(after.args[i])
		if before.line[beforeCursor:beforeStart] != after.line[afterCursor:afterStart] {
			return fmt.Errorf("non-token bytes changed")
		}
		beforeCursor, afterCursor = beforeEnd, afterEnd
	}
	if before.line[beforeCursor:] != after.line[afterCursor:] {
		return fmt.Errorf("trailing bytes changed")
	}
	return nil
}

func unsafeCorrectionWrapper(command string) bool {
	switch command {
	case "sudo", "doas", "eval", "env", "command", "builtin", "exec", "xargs":
		return true
	default:
		return false
	}
}

func parseSimpleCommandShape(line, dialect string) (simpleCommandShape, error) { //nolint:gocyclo // shell AST eligibility is a linear safety checklist
	lang := syntax.LangBash
	if strings.EqualFold(strings.TrimSpace(dialect), "zsh") {
		lang = syntax.LangZsh
	} else if dialect != "" && !strings.EqualFold(strings.TrimSpace(dialect), "bash") {
		return simpleCommandShape{}, fmt.Errorf("unsupported dialect %q", dialect)
	}
	file, err := syntax.NewParser(syntax.Variant(lang)).Parse(strings.NewReader(line), "correction")
	if err != nil {
		return simpleCommandShape{}, fmt.Errorf("parse correction: %w", err)
	}
	if len(file.Stmts) != 1 {
		return simpleCommandShape{}, fmt.Errorf("correction must contain one statement")
	}
	stmt := file.Stmts[0]
	if stmt.Negated || stmt.Background || stmt.Coprocess || stmt.Disown {
		return simpleCommandShape{}, fmt.Errorf("compound execution is ineligible")
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) == 0 {
		return simpleCommandShape{}, fmt.Errorf("correction must be one simple command")
	}
	unsafe := false
	syntax.Walk(file, func(node syntax.Node) bool {
		switch node.(type) {
		case *syntax.CmdSubst, *syntax.ParamExp, *syntax.ArithmExp, *syntax.ProcSubst:
			unsafe = true
		}
		return !unsafe
	})
	if unsafe {
		return simpleCommandShape{}, fmt.Errorf("dynamic shell expressions are ineligible")
	}
	for _, redir := range stmt.Redirs {
		if redir.Hdoc != nil {
			return simpleCommandShape{}, fmt.Errorf("heredocs are ineligible")
		}
	}
	lits := make([]string, len(call.Args))
	for i, word := range call.Args {
		var static bool
		lits[i], static = staticWord(word)
		if !static {
			return simpleCommandShape{}, fmt.Errorf("dynamic shell word is ineligible")
		}
	}
	return simpleCommandShape{line: line, args: call.Args, lits: lits}, nil
}

func staticWord(word *syntax.Word) (string, bool) {
	var value strings.Builder
	for _, part := range word.Parts {
		switch part := part.(type) {
		case *syntax.Lit:
			value.WriteString(part.Value)
		case *syntax.SglQuoted:
			if part.Dollar {
				return "", false
			}
			value.WriteString(part.Value)
		case *syntax.DblQuoted:
			if part.Dollar {
				return "", false
			}
			for _, inner := range part.Parts {
				lit, ok := inner.(*syntax.Lit)
				if !ok {
					return "", false
				}
				value.WriteString(lit.Value)
			}
		default:
			return "", false
		}
	}
	return value.String(), true
}

func wordOffsets(word *syntax.Word) (start, end int) {
	// Parser positions originate from indexes into an in-memory Go string, so
	// they are necessarily representable as int on the current architecture.
	return int(word.Pos().Offset()), int(word.End().Offset()) //nolint:gosec // G115: parser offsets are bounded by string length
}

func sourceForWord(line string, word *syntax.Word) string {
	start, end := wordOffsets(word)
	return line[start:end]
}
