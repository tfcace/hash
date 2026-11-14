package learning

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func openStore(t *testing.T, path string) *FixStore {
	t.Helper()
	store, err := NewFixStore(path)
	if err != nil {
		t.Fatalf("NewFixStore() error = %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func record(t *testing.T, store *FixStore, p Pattern, fix string, successes, failures int) {
	t.Helper()
	for i := 0; i < successes; i++ {
		if err := store.RecordFix(p, fix, true); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < failures; i++ {
		if err := store.RecordFix(p, fix, false); err != nil {
			t.Fatal(err)
		}
	}
}

// The best fix overall can be unrelated to this failure (whatever the user
// ran next once); a related, slightly lower-ranked fix must still be offered.
func TestGetRelatedFix_FallsBackToRelatedCandidate(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "learning.db"))
	p := Pattern{CommandPattern: "{script}", ErrorPattern: "permission denied", ExitCode: 126}
	record(t, store, p, "claude", 6, 0)
	record(t, store, p, "chmod +x deploy.sh", 5, 0)

	fix, found := store.GetRelatedFix(p, "./deploy.sh")
	if !found || fix.Fix != "chmod +x deploy.sh" {
		t.Fatalf("GetRelatedFix() = %q, %v, want the related chmod fix", fix.Fix, found)
	}
	if want := calculateScore(fix.SuccessCount, fix.FailureCount, fix.LastUsed); fix.Score < want-1e-6 || fix.Score > want+1e-6 {
		t.Errorf("Score = %v, want calculateScore of the row", fix.Score)
	}
	if _, found := store.GetRelatedFix(p, "./other.sh"); found {
		t.Error("GetRelatedFix() offered a fix unrelated to ./other.sh")
	}
}

// GetFix returns the candidate calculateScore ranks highest; the SQL order
// (rate, then count) disagreed with it.
func TestGetFix_RanksByScore(t *testing.T) {
	store := openStore(t, filepath.Join(t.TempDir(), "learning.db"))
	p := Pattern{CommandPattern: "make", ErrorPattern: "missing", ExitCode: 2}
	record(t, store, p, "make deps", 1, 0)    // rate 1/2 in SQL, score ~0.72
	record(t, store, p, "make clean", 30, 10) // rate 30/41 in SQL, score ~0.81

	_, err := store.db.Exec(`UPDATE fixes SET last_used = datetime('now', '-400 days') WHERE fix = 'make clean'`)
	if err != nil {
		t.Fatal(err)
	}
	want := "make deps"
	if calculateScore(30, 10, mustLastUsed(t, store, "make clean")) > calculateScore(1, 0, mustLastUsed(t, store, "make deps")) {
		want = "make clean"
	}
	fix, found := store.GetFix(p)
	if !found || fix.Fix != want {
		t.Fatalf("GetFix() = %q, want %q (highest calculateScore)", fix.Fix, want)
	}
}

func mustLastUsed(t *testing.T, store *FixStore, fix string) (lastUsed time.Time) {
	t.Helper()
	if err := store.db.QueryRow(`SELECT last_used FROM fixes WHERE fix = ?`, fix).Scan(&lastUsed); err != nil {
		t.Fatal(err)
	}
	return lastUsed
}

// Two shells recording the same new fix at once: update-then-insert raced to
// a UNIQUE constraint error and lost the count.
func TestRecordFix_ConcurrentShellsCountEveryAttempt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "learning.db")
	stores := []*FixStore{openStore(t, path), openStore(t, path)}
	p := Pattern{CommandPattern: "{script}", ErrorPattern: "permission denied", ExitCode: 126}

	const perStore = 20
	var wg sync.WaitGroup
	errs := make(chan error, 2*perStore)
	for _, s := range stores {
		for i := 0; i < perStore; i++ {
			wg.Add(1)
			go func(s *FixStore) {
				defer wg.Done()
				errs <- s.RecordFix(p, "chmod +x deploy.sh", true)
			}(s)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("RecordFix() error = %v", err)
		}
	}
	fix, _ := stores[0].GetFix(p)
	if fix.SuccessCount != 2*perStore {
		t.Errorf("SuccessCount = %d, want %d", fix.SuccessCount, 2*perStore)
	}
}
