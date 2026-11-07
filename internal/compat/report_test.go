// internal/compat/report_test.go
package compat

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/executor"
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
	mtime := time.Now().Truncate(time.Second)
	state := &State{
		Status:      StatusImported,
		SourceShell: "zsh",
		Paths:       []string{"/home/u/.zshrc"},
		Import: &ImportRecord{
			Time:    time.Now().Truncate(time.Second),
			Summary: Summary{Aliases: 5, Exports: 3, Functions: 2, Skipped: 10},
			Mtimes:  map[string]time.Time{"/home/u/.zshrc": mtime},
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

	if len(loaded.Paths) != 1 || loaded.Paths[0] != "/home/u/.zshrc" || !loaded.Import.Mtimes["/home/u/.zshrc"].Equal(mtime) {
		t.Errorf("Paths = %v, Mtimes = %v, want %v and %v", loaded.Paths, loaded.Import.Mtimes, state.Paths, mtime)
	}
	if loaded.Import.Summary.Aliases != 5 {
		t.Errorf("Summary.Aliases = %d, want 5", loaded.Import.Summary.Aliases)
	}
}

func TestState_LoadNonExistent(t *testing.T) {
	_, err := LoadState("/nonexistent/path/migration.json")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

// A file the filter stops in at a statement that does not parse is not
// loaded, or not from that statement on: the report says so, and does not
// count it a skipped item. The migration said "1 item skipped" of a
// .bashrc nothing in ran.
func TestFilter_AFileThatDoesNotParseIsNotLoaded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".bashrc")
	writeFile(t, path, "export A=1\nalias k=kubectl\n}\n")
	_, report, err := FilterWithDialect(path, "bash", "bash")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.NotLoaded) != 1 || report.Summary.Skipped != 0 {
		t.Fatalf("not loaded %+v, skipped %d, want the file not loaded and nothing skipped", report.NotLoaded, report.Summary.Skipped)
	}
	n := report.NotLoaded[0]
	if want := "~/.bashrc: not loaded: line 3: " + n.Reason; n.File != path || n.Line != 3 || n.Load != executor.LoadedNone || n.Reason == "" || n.String() != want {
		t.Errorf("not loaded %+v (%q), want line 3 of %s, nothing run: %q", n, n.String(), path, want)
	}

	_, report = filterRC("echo a\necho b\nif true; then\n", "", "bash", func() *Report { return NewReport("", "") })
	if len(report.NotLoaded) != 1 || report.NotLoaded[0].Load != executor.LoadedBefore || report.NotLoaded[0].Line != 3 ||
		!strings.Contains(report.NotLoaded[0].String(), "not loaded from line 3") {
		t.Errorf("a statement that never ends: not loaded %+v, want the lines before it run", report.NotLoaded)
	}
}
