// internal/compat/integration_test.go
package compat

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFullMigrationFlow(t *testing.T) {
	tmpDir := t.TempDir()

	// Set up test environment
	home := tmpDir
	dataDir := filepath.Join(tmpDir, ".local", "share", "hash")
	os.MkdirAll(dataDir, 0755)

	// Create a realistic .zshrc
	zshrc := filepath.Join(home, ".zshrc")
	content := `# My zsh config
export EDITOR=vim
export PATH="$HOME/bin:$PATH"

alias ll='ls -la'
alias gs='git status'
alias gc='git commit'

# zsh-specific stuff (should be skipped)
bindkey '^R' history-incremental-search-backward
setopt AUTO_CD
setopt HIST_IGNORE_DUPS
autoload -Uz compinit && compinit

myfunc() {
    echo "Hello $1"
}
`
	if err := os.WriteFile(zshrc, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create .zshrc: %v", err)
	}

	// Run migration
	report, err := SourceWithCompat(context.Background(), zshrc, "zsh", nil)
	if err != nil {
		t.Fatalf("SourceWithCompat() error = %v", err)
	}

	// Verify imports
	if report.Summary.Aliases < 3 {
		t.Errorf("Aliases = %d, want >= 3", report.Summary.Aliases)
	}
	if report.Summary.Exports < 2 {
		t.Errorf("Exports = %d, want >= 2", report.Summary.Exports)
	}

	// Verify skips
	if report.Summary.Skipped < 4 {
		t.Errorf("Skipped = %d, want >= 4 (bindkey, setopt x2, autoload)", report.Summary.Skipped)
	}

	// Verify specific skipped items
	foundBindkey := false
	foundSetopt := false
	for _, item := range report.SkippedItems {
		if item.Content == "bindkey '^R' history-incremental-search-backward" ||
			(len(item.Content) > 7 && item.Content[:7] == "bindkey") {
			foundBindkey = true
		}
		if len(item.Content) > 6 && item.Content[:6] == "setopt" {
			foundSetopt = true
		}
	}
	if !foundBindkey {
		t.Error("expected bindkey to be skipped")
	}
	if !foundSetopt {
		t.Error("expected setopt to be skipped")
	}

	// Save and reload state
	statePath := filepath.Join(dataDir, "migration.json")
	state := &State{
		SourceFile:  zshrc,
		SourceShell: "zsh",
		SourceMtime: report.SourceMtime,
		LastImport:  report.ImportTime,
		Summary:     report.Summary,
	}
	if err := state.Save(statePath); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if loaded.Summary.Aliases != report.Summary.Aliases {
		t.Errorf("loaded aliases = %d, want %d", loaded.Summary.Aliases, report.Summary.Aliases)
	}
}
