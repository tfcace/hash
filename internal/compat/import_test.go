// internal/compat/import_test.go
package compat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

func TestSourceWithCompat_Aliases(t *testing.T) {
	tmpDir := t.TempDir()
	rcFile := filepath.Join(tmpDir, ".zshrc")

	content := `# Test zshrc
alias ll='ls -la'
alias gs='git status'
export EDITOR=vim
`
	os.WriteFile(rcFile, []byte(content), 0644)

	report, err := SourceWithCompat(context.Background(), rcFile, "zsh", nil)
	if err != nil {
		t.Fatalf("SourceWithCompat() error = %v", err)
	}

	// Should have imported aliases and export
	if report.Summary.Aliases < 2 {
		t.Errorf("Aliases = %d, want >= 2", report.Summary.Aliases)
	}
	if report.Summary.Exports < 1 {
		t.Errorf("Exports = %d, want >= 1", report.Summary.Exports)
	}
}

func TestSourceWithCompat_SkipsZshBuiltins(t *testing.T) {
	tmpDir := t.TempDir()
	rcFile := filepath.Join(tmpDir, ".zshrc")

	content := `# Test zshrc with zsh-specific commands
bindkey '^R' history-incremental-search-backward
setopt AUTO_CD
alias ll='ls -la'
`
	os.WriteFile(rcFile, []byte(content), 0644)

	report, err := SourceWithCompat(context.Background(), rcFile, "zsh", nil)
	if err != nil {
		t.Fatalf("SourceWithCompat() error = %v", err)
	}

	// Should have skipped bindkey and setopt
	if report.Summary.Skipped < 2 {
		t.Errorf("Skipped = %d, want >= 2", report.Summary.Skipped)
	}
	// Should still import the alias
	if report.Summary.Aliases < 1 {
		t.Errorf("Aliases = %d, want >= 1", report.Summary.Aliases)
	}
}

func TestSourceWithCompat_HandlesBashSyntax(t *testing.T) {
	tmpDir := t.TempDir()
	rcFile := filepath.Join(tmpDir, ".zshrc")

	// Bash syntax like [[ ]] and == should work (mvdan/sh supports LangBash)
	content := `# Test zshrc with bash syntax
alias ll='ls -la'
[[ -n "$TERM" ]] && export HAS_TERM=1
if [[ "$FOO" == "bar" ]]; then export IS_BAR=1; fi
export EDITOR=vim
`
	os.WriteFile(rcFile, []byte(content), 0644)

	report, err := SourceWithCompat(context.Background(), rcFile, "zsh", nil)
	if err != nil {
		t.Fatalf("SourceWithCompat() error = %v", err)
	}

	// Bash syntax should be handled, not skipped
	// Should import all the exports and alias
	if report.Summary.Aliases < 1 {
		t.Errorf("Aliases = %d, want >= 1", report.Summary.Aliases)
	}
	if report.Summary.Exports < 1 {
		t.Errorf("Exports = %d, want >= 1", report.Summary.Exports)
	}
}

func TestSourceWithCompat_SkipsStarshipInit(t *testing.T) {
	tmpDir := t.TempDir()
	rcFile := filepath.Join(tmpDir, ".zshrc")

	content := `# Test zshrc with starship
alias ll='ls -la'
eval "$(starship init zsh)"
export EDITOR=vim
`
	os.WriteFile(rcFile, []byte(content), 0644)

	report, err := SourceWithCompat(context.Background(), rcFile, "zsh", nil)
	if err != nil {
		t.Fatalf("SourceWithCompat() error = %v", err)
	}

	// Should have skipped starship init
	foundStarship := false
	for _, item := range report.SkippedItems {
		if strings.Contains(item.Content, "starship") {
			foundStarship = true
			break
		}
	}
	if !foundStarship {
		t.Error("expected starship init to be skipped")
	}

	// Should still import the alias and export
	if report.Summary.Aliases < 1 {
		t.Errorf("Aliases = %d, want >= 1", report.Summary.Aliases)
	}
	if report.Summary.Exports < 1 {
		t.Errorf("Exports = %d, want >= 1", report.Summary.Exports)
	}
}

func TestFilterWithDialect_ZshKeepsZshInitLines(t *testing.T) {
	tmpDir := t.TempDir()
	rcFile := filepath.Join(tmpDir, ".zshrc")

	content := `# zsh mode should keep zsh eval/source lines
eval "$(zoxide init zsh)"
source ~/.zsh/zsh-autosuggestions/zsh-autosuggestions.zsh
setopt AUTO_CD
`
	if err := os.WriteFile(rcFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write rc file: %v", err)
	}

	bashFiltered, _, err := FilterWithCompat(rcFile, "zsh")
	if err != nil {
		t.Fatalf("FilterWithCompat() error = %v", err)
	}
	if !strings.Contains(bashFiltered, "# [hash-compat] eval \"$(zoxide init zsh)\"") {
		t.Fatalf("bash compatibility should still comment zsh init lines, got:\n%s", bashFiltered)
	}

	zshFiltered, report, err := FilterWithDialect(rcFile, "zsh", "zsh")
	if err != nil {
		t.Fatalf("FilterWithDialect(zsh) error = %v", err)
	}
	if strings.Contains(zshFiltered, "# [hash-compat] eval \"$(zoxide init zsh)\"") {
		t.Fatalf("zsh dialect should keep zsh init lines, got:\n%s", zshFiltered)
	}
	if strings.Contains(zshFiltered, "# [hash-compat] source ~/.zsh/zsh-autosuggestions") {
		t.Fatalf("zsh dialect should keep zsh source lines, got:\n%s", zshFiltered)
	}
	if !strings.Contains(zshFiltered, "# [hash-compat] setopt AUTO_CD") {
		t.Fatalf("zsh builtins without runtime support should still be no-oped, got:\n%s", zshFiltered)
	}
	if report.Summary.Skipped != 1 {
		t.Fatalf("Skipped = %d, want 1 for setopt only", report.Summary.Skipped)
	}
}

func TestFilterWithDialect_SkippedLineInsideIfLeavesStatement(t *testing.T) {
	tmpDir := t.TempDir()
	rcFile := filepath.Join(tmpDir, ".zshrc")
	content := `if [[ "${TERM:-}" != "dumb" ]]; then
  _cached_eval starship starship init zsh
fi
`
	if err := os.WriteFile(rcFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	filtered, report, err := FilterWithDialect(rcFile, "zsh", "bash")
	if err != nil {
		t.Fatalf("FilterWithDialect error = %v", err)
	}
	if report.Summary.Skipped != 1 {
		t.Fatalf("Skipped = %d, want 1", report.Summary.Skipped)
	}

	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	if _, err := parser.Parse(strings.NewReader(filtered), rcFile); err != nil {
		t.Fatalf("filtered startup file should parse, got %v\nfiltered:\n%s", err, filtered)
	}
}

func TestFilterWithDialect_BashSkipsPowerlevel10kInstantPromptBlock(t *testing.T) {
	tmpDir := t.TempDir()
	rcFile := filepath.Join(tmpDir, ".zshrc")
	content := `if [[ -r "${XDG_CACHE_HOME:-$HOME/.cache}/p10k-instant-prompt-${(%):-%n}.zsh" ]]; then
  source "${XDG_CACHE_HOME:-$HOME/.cache}/p10k-instant-prompt-${(%):-%n}.zsh"
fi
export PATH="$HOME/.local/bin:$PATH"
`
	if err := os.WriteFile(rcFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	filtered, report, err := FilterWithDialect(rcFile, "zsh", "bash")
	if err != nil {
		t.Fatalf("FilterWithDialect error = %v", err)
	}
	if report.Summary.Skipped != 2 {
		t.Fatalf("Skipped = %d, want 2", report.Summary.Skipped)
	}
	if !strings.Contains(filtered, "if false; then # [hash-compat]") {
		t.Fatalf("filtered startup file should disable the zsh conditional:\n%s", filtered)
	}

	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	if _, err := parser.Parse(strings.NewReader(filtered), rcFile); err != nil {
		t.Fatalf("filtered startup file should parse as bash, got %v\nfiltered:\n%s", err, filtered)
	}

	zshFiltered, zshReport, err := FilterWithDialect(rcFile, "zsh", "zsh")
	if err != nil {
		t.Fatalf("FilterWithDialect(zsh) error = %v", err)
	}
	if zshReport.Summary.Skipped != 0 {
		t.Fatalf("zsh Skipped = %d, want 0", zshReport.Summary.Skipped)
	}
	if !strings.Contains(zshFiltered, "${(%):-%n}") {
		t.Fatalf("zsh startup file should retain zsh parameter expansion:\n%s", zshFiltered)
	}
}

func TestFilterWithDialect_BashSkipsPowerlevel10kMultilineThen(t *testing.T) {
	tmpDir := t.TempDir()
	rcFile := filepath.Join(tmpDir, ".zshrc")
	content := `if [[ -r "${XDG_CACHE_HOME:-$HOME/.cache}/p10k-instant-prompt-${(%):-%n}.zsh" ]]
then
  source "${XDG_CACHE_HOME:-$HOME/.cache}/p10k-instant-prompt-${(%):-%n}.zsh"
fi
`
	if err := os.WriteFile(rcFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	filtered, report, err := FilterWithDialect(rcFile, "zsh", "bash")
	if err != nil {
		t.Fatalf("FilterWithDialect error = %v", err)
	}
	if report.Summary.Skipped != 2 {
		t.Fatalf("Skipped = %d, want 2", report.Summary.Skipped)
	}

	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	if _, err := parser.Parse(strings.NewReader(filtered), rcFile); err != nil {
		t.Fatalf("filtered startup file should parse as bash, got %v\nfiltered:\n%s", err, filtered)
	}
}

func TestFilterWithDialect_BashSkipsPowerlevel10kInlineThenWhitespace(t *testing.T) {
	tmpDir := t.TempDir()
	rcFile := filepath.Join(tmpDir, ".zshrc")
	content := `if [[ -r "${XDG_CACHE_HOME:-$HOME/.cache}/p10k-instant-prompt-${(%):-%n}.zsh" ]];  then # optional comment
  source "${XDG_CACHE_HOME:-$HOME/.cache}/p10k-instant-prompt-${(%):-%n}.zsh"
fi
`
	if err := os.WriteFile(rcFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	filtered, report, err := FilterWithDialect(rcFile, "zsh", "bash")
	if err != nil {
		t.Fatalf("FilterWithDialect error = %v", err)
	}
	if report.Summary.Skipped != 2 {
		t.Fatalf("Skipped = %d, want 2", report.Summary.Skipped)
	}

	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	if _, err := parser.Parse(strings.NewReader(filtered), rcFile); err != nil {
		t.Fatalf("filtered startup file should parse as bash, got %v\nfiltered:\n%s", err, filtered)
	}
}

func TestCheckSourceChanged(t *testing.T) {
	tmpDir := t.TempDir()
	rcFile := filepath.Join(tmpDir, ".zshrc")
	statePath := filepath.Join(tmpDir, "migration.json")

	// Create rc file
	os.WriteFile(rcFile, []byte("alias ll='ls -la'\n"), 0644)

	// Create state with old mtime
	state := &State{
		SourceFile:  rcFile,
		SourceMtime: time.Now().Add(-time.Hour),
	}
	state.Save(statePath)

	// File should be detected as changed
	changed, err := CheckSourceChanged(statePath)
	if err != nil {
		t.Fatalf("CheckSourceChanged() error = %v", err)
	}
	if !changed {
		t.Error("expected file to be detected as changed")
	}
}
