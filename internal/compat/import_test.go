// internal/compat/import_test.go
package compat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/executor"
	"mvdan.cc/sh/v3/syntax"
)

func TestFilterWithDialect_Aliases(t *testing.T) {
	tmpDir := t.TempDir()
	rcFile := filepath.Join(tmpDir, ".zshrc")

	content := `# Test zshrc
alias ll='ls -la'
alias gs='git status'
export EDITOR=vim
`
	os.WriteFile(rcFile, []byte(content), 0644)

	_, report, err := FilterWithDialect(rcFile, "zsh", "bash")
	if err != nil {
		t.Fatalf("FilterWithDialect() error = %v", err)
	}

	// Should have imported aliases and export
	if report.Summary.Aliases < 2 {
		t.Errorf("Aliases = %d, want >= 2", report.Summary.Aliases)
	}
	if report.Summary.Exports < 1 {
		t.Errorf("Exports = %d, want >= 1", report.Summary.Exports)
	}
}

func TestFilterWithDialect_SkipsZshBuiltins(t *testing.T) {
	tmpDir := t.TempDir()
	rcFile := filepath.Join(tmpDir, ".zshrc")

	content := `# Test zshrc with zsh-specific commands
bindkey '^R' history-incremental-search-backward
setopt AUTO_CD
alias ll='ls -la'
`
	os.WriteFile(rcFile, []byte(content), 0644)

	_, report, err := FilterWithDialect(rcFile, "zsh", "bash")
	if err != nil {
		t.Fatalf("FilterWithDialect() error = %v", err)
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

func TestFilterWithDialect_HandlesBashSyntax(t *testing.T) {
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

	_, report, err := FilterWithDialect(rcFile, "zsh", "bash")
	if err != nil {
		t.Fatalf("FilterWithDialect() error = %v", err)
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

func TestFilterWithDialect_SkipsStarshipInit(t *testing.T) {
	tmpDir := t.TempDir()
	rcFile := filepath.Join(tmpDir, ".zshrc")

	content := `# Test zshrc with starship
alias ll='ls -la'
eval "$(starship init zsh)"
export EDITOR=vim
`
	os.WriteFile(rcFile, []byte(content), 0644)

	_, report, err := FilterWithDialect(rcFile, "zsh", "bash")
	if err != nil {
		t.Fatalf("FilterWithDialect() error = %v", err)
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

	bashFiltered, _, err := FilterWithDialect(rcFile, "zsh", "bash")
	if err != nil {
		t.Fatalf("FilterWithDialect() error = %v", err)
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
	if report.Summary.Skipped != 1 || report.SkippedItems[0].Line != 1 {
		t.Fatalf("SkippedItems = %+v, want the if on line 1", report.SkippedItems)
	}
	if want := "# [hash-compat] if [[ -r"; !strings.HasPrefix(filtered, want) || !strings.Contains(filtered, "\n: # [hash-compat] fi\nexport PATH=") {
		t.Fatalf("filtered startup file should comment out the zsh conditional:\n%s", filtered)
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
	if report.Summary.Skipped != 1 {
		t.Fatalf("Skipped = %d, want 1 (the if)", report.Summary.Skipped)
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
	if report.Summary.Skipped != 1 {
		t.Fatalf("Skipped = %d, want 1 (the if)", report.Summary.Skipped)
	}

	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	if _, err := parser.Parse(strings.NewReader(filtered), rcFile); err != nil {
		t.Fatalf("filtered startup file should parse as bash, got %v\nfiltered:\n%s", err, filtered)
	}
}

// A bug in the filter must never crash startup: the file it fails on is
// reported as not loaded, with the error, and the other files still load.
func TestFilterFiles_FilterPanicIsAFileNotLoaded(t *testing.T) {
	dir := t.TempDir()
	bad, good := filepath.Join(dir, ".zshrc"), filepath.Join(dir, ".zprofile")
	for _, p := range []string{bad, good} {
		if err := os.WriteFile(p, []byte("export A=1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	real := filterContent
	t.Cleanup(func() { filterContent = real })
	filterContent = func(content, shell, dialect string, newReport func() *Report) (string, *Report) {
		if strings.Contains(content, "boom") {
			panic("filter bug")
		}
		return real(content, shell, dialect, newReport)
	}
	if err := os.WriteFile(bad, []byte("boom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Its callers name the file: the error does not.
	if _, _, err := FilterWithDialect(bad, "zsh", "bash"); err == nil || !strings.Contains(err.Error(), "filter bug") || strings.Contains(err.Error(), bad) {
		t.Fatalf("err = %v, want the filter's panic as an error, without the path", err)
	}
	t.Setenv("HOME", dir)
	files, report, errs := FilterFiles([]string{bad, good}, "zsh", "bash")
	if len(errs) != 1 || len(files) != 1 || files[0].Path != good || report == nil || report.Summary.Exports != 1 {
		t.Fatalf("files %+v, errs %v, report %+v", files, errs, report)
	}
	if msg := errs[0].Error(); !strings.HasPrefix(msg, "~/.zshrc: not loaded") || strings.Count(msg, ".zshrc") != 1 {
		t.Errorf("FilterFiles error %q, want the file named once", msg)
	}
}

// A file sourced at runtime is filtered when it is a zsh file and the
// dialect bash; a filter failure loads none of it.
func TestFilterSourced_ZshFilesInTheBashDialect(t *testing.T) {
	const src = "setopt autocd\nalias k=kubectl\n"
	for _, tt := range []struct{ path, dialect, want string }{
		{"/p/x.plugin.zsh", "bash", ": # [hash-compat] setopt autocd\nalias k=kubectl\n"},
		{"/p/.zshrc.local", "", ": # [hash-compat] setopt autocd\nalias k=kubectl\n"},
		{"/p/x.zsh", "zsh", src},
		{"/p/x.sh", "bash", src},
		{"/p/x", "bash", src},
	} {
		got, n := FilterSourced(tt.path, []byte(src), tt.dialect, "")
		if n != nil || string(got) != tt.want {
			t.Errorf("%s (%s): %q %v, want %q", tt.path, tt.dialect, got, n, tt.want)
		}
	}
	real := filterContent
	t.Cleanup(func() { filterContent = real })
	filterContent = func(string, string, string, func() *Report) (string, *Report) { panic("filter bug") }
	if _, n := FilterSourced("/p/x.zsh", []byte(src), "bash", ""); n == nil || n.Load != executor.LoadedNone || n.Line != 0 ||
		n.String() != "/p/x.zsh: not loaded: the compatibility filter failed: filter bug" {
		t.Errorf("not loaded %+v, want the filter's failure, none of the file loaded", n)
	}
}

// A file sourced in a file written for a shell is filtered as that shell's
// when it is a startup file of the shell too (the .bashrc a migrated
// .bash_profile sources): its exec of another shell is skipped. Elsewhere,
// and a script of the shell's (nvm.sh), it is as it is: every *.sh a
// migrated .bashrc sourced went through the filter.
func TestFilterSourced_AFileOfTheSourcingFilesShell(t *testing.T) {
	const src = "alias k=kubectl\nexec zsh\n"
	for _, tt := range []struct {
		path, from string
		filtered   bool
	}{
		{"/p/.bashrc", "bash", true},
		{"/p/.bashrc", "", false},
		{"/p/.bashrc", "zsh", false},
		{"/p/.bash_aliases", "bash", false},
		{"/p/.nvm/nvm.sh", "bash", false},
		{"/p/lib.bash", "bash", false},
		{"/p/.zshrc.local", "zsh", true},
	} {
		got, n := FilterSourced(tt.path, []byte(src), "bash", tt.from)
		if n != nil || (string(got) != src) == !tt.filtered || (tt.filtered && strings.Contains(string(got), "\nexec zsh")) {
			t.Errorf("%s in a %q file: %q %v, want it filtered %v", tt.path, tt.from, got, n, tt.filtered)
		}
	}
}
