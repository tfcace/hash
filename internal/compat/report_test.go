// internal/compat/report_test.go
package compat

import (
	"path/filepath"
	"testing"
	"time"
)

func TestReport_AddImported(t *testing.T) {
	r := NewReport("~/.zshrc", "zsh")

	r.AddImported(ItemAlias, "ll", "ls -la")
	r.AddImported(ItemExport, "EDITOR", "vim")
	r.AddImported(ItemFunction, "mkcd", "")

	if r.Summary.Aliases != 1 {
		t.Errorf("Aliases = %d, want 1", r.Summary.Aliases)
	}
	if r.Summary.Exports != 1 {
		t.Errorf("Exports = %d, want 1", r.Summary.Exports)
	}
	if r.Summary.Functions != 1 {
		t.Errorf("Functions = %d, want 1", r.Summary.Functions)
	}
}

func TestReport_AddSkipped(t *testing.T) {
	r := NewReport("~/.zshrc", "zsh")

	r.AddSkipped(12, "bindkey '^R' history-search", "zsh-specific builtin")
	r.AddSkipped(15, "setopt AUTO_CD", "zsh-specific builtin")

	if r.Summary.Skipped != 2 {
		t.Errorf("Skipped = %d, want 2", r.Summary.Skipped)
	}
	if len(r.SkippedItems) != 2 {
		t.Errorf("SkippedItems len = %d, want 2", len(r.SkippedItems))
	}
	if r.SkippedItems[0].Line != 12 {
		t.Errorf("SkippedItems[0].Line = %d, want 12", r.SkippedItems[0].Line)
	}
}

func TestState_SaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "migration.json")

	// Create and save state
	state := &State{
		SourceFile:  "~/.zshrc",
		SourceShell: "zsh",
		SourceMtime: time.Now().Truncate(time.Second),
		LastImport:  time.Now().Truncate(time.Second),
		Declined:    false,
		Summary: Summary{
			Aliases:   5,
			Exports:   3,
			Functions: 2,
			Skipped:   10,
		},
	}

	if err := state.Save(statePath); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Load and verify
	loaded, err := LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}

	if loaded.SourceFile != state.SourceFile {
		t.Errorf("SourceFile = %q, want %q", loaded.SourceFile, state.SourceFile)
	}
	if loaded.Summary.Aliases != 5 {
		t.Errorf("Summary.Aliases = %d, want 5", loaded.Summary.Aliases)
	}
}

func TestState_LoadNonExistent(t *testing.T) {
	_, err := LoadState("/nonexistent/path/migration.json")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}
