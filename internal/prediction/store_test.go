package prediction

import (
	"path/filepath"
	"testing"
)

func TestStore_RecordAndGetSequence(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(filepath.Join(tmpDir, "prediction.db"))
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	defer store.Close()

	// Record a sequence
	err = store.RecordSequence("git pull", "npm test", "/project")
	if err != nil {
		t.Fatalf("RecordSequence error: %v", err)
	}

	// Record again to increase count
	store.RecordSequence("git pull", "npm test", "/project")

	// Get sequences
	seqs, err := store.GetSequences("git pull", "/project")
	if err != nil {
		t.Fatalf("GetSequences error: %v", err)
	}

	if len(seqs) != 1 {
		t.Fatalf("Expected 1 sequence, got %d", len(seqs))
	}
	if seqs[0].Count != 2 {
		t.Errorf("Count = %d, want 2", seqs[0].Count)
	}
}

func TestStore_RecordAndGetPathUsage(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(filepath.Join(tmpDir, "prediction.db"))
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	defer store.Close()

	// Record path usage
	store.RecordPathUsage("vim", "src/main.go", "/project")
	store.RecordPathUsage("vim", "src/main.go", "/project")
	store.RecordPathUsage("vim", "src/config.go", "/project")

	// Get paths for vim
	paths, err := store.GetPathsForCommand("vim", "/project")
	if err != nil {
		t.Fatalf("GetPathsForCommand error: %v", err)
	}

	if len(paths) != 2 {
		t.Fatalf("Expected 2 paths, got %d", len(paths))
	}
	// First should be most used
	if paths[0].Path != "src/main.go" {
		t.Errorf("First path = %q, want src/main.go", paths[0].Path)
	}
}
