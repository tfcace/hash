package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStore_CreateAndAdd(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "history.db")

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	cmd := Command{
		Command:    "echo hello",
		Cwd:        "/home/user",
		ExitCode:   0,
		DurationMs: 10,
		Timestamp:  time.Now(),
	}

	id, err := store.Add(cmd)
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if id <= 0 {
		t.Errorf("ID = %d, want > 0", id)
	}
}

func TestStore_GetRecent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "history.db")

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	// Add some commands
	for i := 0; i < 5; i++ {
		store.Add(Command{
			Command:   "echo " + string(rune('a'+i)),
			Cwd:       "/home/user",
			Timestamp: time.Now(),
		})
	}

	commands, err := store.GetRecent(3)
	if err != nil {
		t.Fatalf("GetRecent() error = %v", err)
	}

	if len(commands) != 3 {
		t.Errorf("Count = %d, want 3", len(commands))
	}

	// Most recent should be first
	if commands[0].Command != "echo e" {
		t.Errorf("First = %q, want %q", commands[0].Command, "echo e")
	}
}

func TestStore_Search(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "history.db")

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	store.Add(Command{Command: "kubectl get pods", Cwd: "/", Timestamp: time.Now()})
	store.Add(Command{Command: "docker ps", Cwd: "/", Timestamp: time.Now()})
	store.Add(Command{Command: "kubectl get services", Cwd: "/", Timestamp: time.Now()})

	results, err := store.Search(SearchOptions{Query: "kubectl"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Count = %d, want 2", len(results))
	}
}

func TestStore_SudoCommands(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "history.db")

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	store.Add(Command{
		Command:    "apt-get update",
		IsSudo:     true,
		SudoUser:   "root",
		RawCommand: "sudo apt-get update",
		Cwd:        "/",
		Timestamp:  time.Now(),
	})
	store.Add(Command{Command: "ls", Cwd: "/", Timestamp: time.Now()})

	results, err := store.Search(SearchOptions{OnlySudo: true})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Count = %d, want 1", len(results))
	}
	if !results[0].IsSudo {
		t.Error("Expected sudo command")
	}
}

func TestStore_FileCreated(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "subdir", "history.db")

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	store.Close()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("Database file was not created")
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "history.db")
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestSearchByPrefix_MatchesPrefix(t *testing.T) {
	store := newTestStore(t)

	store.Add(Command{Command: "git status", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})
	store.Add(Command{Command: "git push origin main", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})
	store.Add(Command{Command: "docker ps", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})

	results, err := store.SearchByPrefix("git", 10)
	if err != nil {
		t.Fatalf("SearchByPrefix() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
}

func TestSearchByPrefix_ExcludesFailed(t *testing.T) {
	store := newTestStore(t)

	store.Add(Command{Command: "git push", Cwd: "/", ExitCode: 1, Timestamp: time.Now()})
	store.Add(Command{Command: "git status", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})

	results, err := store.SearchByPrefix("git", 10)
	if err != nil {
		t.Fatalf("SearchByPrefix() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 (only successful)", len(results))
	}
	if results[0] != "git status" {
		t.Errorf("got %q, want %q", results[0], "git status")
	}
}

func TestSearchByPrefix_Deduplicates(t *testing.T) {
	store := newTestStore(t)

	store.Add(Command{Command: "git status", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})
	store.Add(Command{Command: "git status", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})

	results, err := store.SearchByPrefix("git", 10)
	if err != nil {
		t.Fatalf("SearchByPrefix() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 (deduped)", len(results))
	}
}

func TestSearchByPrefix_OrdersByRecent(t *testing.T) {
	store := newTestStore(t)

	store.Add(Command{Command: "git status", Cwd: "/", ExitCode: 0, Timestamp: time.Now().Add(-time.Hour)})
	store.Add(Command{Command: "git push", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})

	results, err := store.SearchByPrefix("git", 10)
	if err != nil {
		t.Fatalf("SearchByPrefix() error = %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0] != "git push" {
		t.Errorf("first result = %q, want %q (most recent)", results[0], "git push")
	}
}

func TestSearchByPrefix_EmptyPrefix(t *testing.T) {
	store := newTestStore(t)

	store.Add(Command{Command: "git status", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})

	results, err := store.SearchByPrefix("", 10)
	if err != nil {
		t.Fatalf("SearchByPrefix() error = %v", err)
	}
	if results != nil {
		t.Errorf("expected nil for empty prefix, got %v", results)
	}
}

func TestSearchByPrefix_GlobEscaping(t *testing.T) {
	store := newTestStore(t)

	store.Add(Command{Command: "echo *", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})
	store.Add(Command{Command: "echo hello", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})

	// Searching for "echo *" should match literally, not as a glob wildcard
	results, err := store.SearchByPrefix("echo *", 10)
	if err != nil {
		t.Fatalf("SearchByPrefix() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0] != "echo *" {
		t.Errorf("got %q, want %q", results[0], "echo *")
	}
}

func TestSearchByPrefix_NoMatch(t *testing.T) {
	store := newTestStore(t)

	store.Add(Command{Command: "git status", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})

	results, err := store.SearchByPrefix("docker", 10)
	if err != nil {
		t.Fatalf("SearchByPrefix() error = %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}
