package history

import (
	"testing"
	"time"
)

func TestPrefixIndex_UnloadedReportsNotOK(t *testing.T) {
	idx := &prefixIndex{}
	if _, ok := idx.search("git", 1); ok {
		t.Fatal("expected ok=false before install")
	}
}

func TestPrefixIndex_LiveRecordOutranksLoaded(t *testing.T) {
	idx := &prefixIndex{}
	idx.record("git branch") // Recorded while the load is still running.
	idx.install([]string{"git actions", "git checkout"}) // Most recent first.

	results, ok := idx.search("git", 3)
	if !ok {
		t.Fatal("expected ok=true after install")
	}
	want := []string{"git branch", "git actions", "git checkout"}
	if len(results) != len(want) {
		t.Fatalf("got %v, want %v", results, want)
	}
	for i := range want {
		if results[i] != want[i] {
			t.Fatalf("got %v, want %v", results, want)
		}
	}
}

func TestPrefixIndex_RecordBumpsExisting(t *testing.T) {
	idx := &prefixIndex{}
	idx.install(nil)
	idx.record("git status")
	idx.record("git push")
	idx.record("git status") // Re-running should make it most recent again.

	results, _ := idx.search("git", 2)
	if len(results) != 2 || results[0] != "git status" || results[1] != "git push" {
		t.Fatalf("got %v, want [git status, git push]", results)
	}
}

func TestPrefixIndex_LimitKeepsMostRecent(t *testing.T) {
	idx := &prefixIndex{}
	idx.install(nil)
	for _, cmd := range []string{"git a", "git b", "git c", "git d"} {
		idx.record(cmd)
	}

	results, _ := idx.search("git", 2)
	if len(results) != 2 || results[0] != "git d" || results[1] != "git c" {
		t.Fatalf("got %v, want [git d, git c]", results)
	}
}

func TestPrefixIndex_PrefixIsLiteral(t *testing.T) {
	idx := &prefixIndex{}
	idx.install([]string{"echo *", "echo hello"})

	results, _ := idx.search("echo *", 10)
	if len(results) != 1 || results[0] != "echo *" {
		t.Fatalf("got %v, want [echo *]", results)
	}
}

func TestSearchByPrefix_ServesFromIndexAfterLoad(t *testing.T) {
	store := newTestStore(t)

	if _, err := store.Add(Command{Command: "git status", Cwd: "/", ExitCode: 0, Timestamp: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if !store.waitPrefixIndex() {
		t.Fatal("prefix index did not load")
	}

	// Closing the database proves the lookup no longer touches SQLite.
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}
	results, err := store.SearchByPrefix("git", 10)
	if err != nil {
		t.Fatalf("SearchByPrefix() error = %v", err)
	}
	if len(results) != 1 || results[0] != "git status" {
		t.Fatalf("got %v, want [git status]", results)
	}
}

func TestSearchByPrefix_SQLFallbackBeforeLoad(t *testing.T) {
	store := newTestStore(t)

	if _, err := store.Add(Command{Command: "git status", Cwd: "/", ExitCode: 0, Timestamp: time.Now()}); err != nil {
		t.Fatal(err)
	}
	store.waitPrefixIndex()
	store.idx = &prefixIndex{} // Pretend the load never finished.

	results, err := store.SearchByPrefix("git", 10)
	if err != nil {
		t.Fatalf("SearchByPrefix() error = %v", err)
	}
	if len(results) != 1 || results[0] != "git status" {
		t.Fatalf("got %v, want [git status]", results)
	}
}

func TestSearchByPrefix_IndexExcludesFailedCommands(t *testing.T) {
	store := newTestStore(t)

	store.Add(Command{Command: "git push", Cwd: "/", ExitCode: 1, Timestamp: time.Now()})
	store.Add(Command{Command: "git status", Cwd: "/", ExitCode: 0, Timestamp: time.Now()})
	if !store.waitPrefixIndex() {
		t.Fatal("prefix index did not load")
	}

	results, err := store.SearchByPrefix("git", 10)
	if err != nil {
		t.Fatalf("SearchByPrefix() error = %v", err)
	}
	if len(results) != 1 || results[0] != "git status" {
		t.Fatalf("got %v, want [git status]", results)
	}
}
