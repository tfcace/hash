package learning

import (
	"path/filepath"
	"testing"
)

func TestFixStore_RecordAndRetrieve(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "learning.db")

	store, err := NewFixStore(dbPath)
	if err != nil {
		t.Fatalf("NewFixStore() error = %v", err)
	}
	defer store.Close()

	// Record a fix
	pattern := Pattern{
		CommandPattern: "{script}",
		ErrorPattern:   "permission denied",
		ExitCode:       126,
	}

	err = store.RecordFix(pattern, "chmod +x {script}", true)
	if err != nil {
		t.Fatalf("RecordFix() error = %v", err)
	}

	// Retrieve fix
	fix, found := store.GetFix(pattern)
	if !found {
		t.Fatal("Fix not found")
	}

	if fix.Fix != "chmod +x {script}" {
		t.Errorf("Fix = %q, want %q", fix.Fix, "chmod +x {script}")
	}
}

func TestFixStore_ScoreCalculation(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "learning.db")

	store, err := NewFixStore(dbPath)
	if err != nil {
		t.Fatalf("NewFixStore() error = %v", err)
	}
	defer store.Close()

	pattern := Pattern{
		CommandPattern: "{script}",
		ErrorPattern:   "permission denied",
		ExitCode:       126,
	}

	// Record multiple successes
	for i := 0; i < 10; i++ {
		store.RecordFix(pattern, "chmod +x {script}", true)
	}

	fix, found := store.GetFix(pattern)
	if !found {
		t.Fatal("Fix not found")
	}

	// Score should be high with 100% success rate
	if fix.Score < 0.7 {
		t.Errorf("Score = %f, want >= 0.7", fix.Score)
	}
}

func TestFixStore_NoFix(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "learning.db")

	store, err := NewFixStore(dbPath)
	if err != nil {
		t.Fatalf("NewFixStore() error = %v", err)
	}
	defer store.Close()

	pattern := Pattern{
		CommandPattern: "nonexistent",
		ErrorPattern:   "unknown",
		ExitCode:       1,
	}

	_, found := store.GetFix(pattern)
	if found {
		t.Error("Should not find fix for unknown pattern")
	}
}
